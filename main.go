package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
	"srujanpakanati.com/go-deployment-reporter/internal"
)

type Info struct {
	DeploymentName     string `json:"deployment_name"`
	ReadyPods          int32  `json:"ready_pods"`
	AverageCPU         string `json:"average_cpu_milli_cores"`
	AverageMemoryMiB   string `json:"average_memory_mib"`
	ConcurrentRequests int64  `json:"concurrent_http_requests"` // New field
}

// Prometheus metrics
var (
	readyPodsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "deployment_ready_pods_total",
			Help: "Number of ready pods in the deployment",
		},
		[]string{"deployment"},
	)

	avgCPUGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "deployment_average_cpu_milli_cores",
			Help: "Average CPU usage in milli cores for the deployment",
		},
		[]string{"deployment"},
	)

	avgMemoryGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "deployment_average_memory_mib",
			Help: "Average memory usage in MiB for the deployment",
		},
		[]string{"deployment"},
	)

	concurrentRequestsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "concurrent_http_requests_total",
			Help: "Number of concurrent HTTP requests for the deployment",
		},
		[]string{"deployment"},
	)

	totalRequestsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests handled",
		},
		[]string{"deployment", "endpoint", "method", "status"},
	)

	metricsCollectionDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name: "metrics_collection_duration_seconds",
			Help: "Duration of metrics collection in seconds",
		},
	)

	metricsCollectionErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "metrics_collection_errors_total",
			Help: "Total number of metrics collection errors",
		},
		[]string{"type"},
	)
)

func init() {
	// Register all metrics with Prometheus
	prometheus.MustRegister(readyPodsGauge)
	prometheus.MustRegister(avgCPUGauge)
	prometheus.MustRegister(avgMemoryGauge)
	prometheus.MustRegister(concurrentRequestsGauge)
	prometheus.MustRegister(totalRequestsCounter)
	prometheus.MustRegister(metricsCollectionDuration)
	prometheus.MustRegister(metricsCollectionErrors)
}

type AppState struct {
	mu               sync.RWMutex
	Info             Info
	clientset        *kubernetes.Clientset
	metricsClientset *metrics.Clientset
	promAPI          prometheusv1.API // New field
	deploymentName   string           // Cache deployment name for metrics
}

// instrumentedResponseWriter wraps http.ResponseWriter to capture status code
type instrumentedResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *instrumentedResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// httpInstrumentationMiddleware tracks concurrent requests and total requests
func (app *AppState) httpInstrumentationMiddleware(endpoint string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get deployment name (use cached value or fallback)
		deploymentName := app.getDeploymentNameForMetrics()

		// Increment concurrent requests
		concurrentRequestsGauge.WithLabelValues(deploymentName).Inc()
		log.Printf("Incremented concurrent requests for deployment: %s", deploymentName)

		defer func() {
			concurrentRequestsGauge.WithLabelValues(deploymentName).Dec()
			log.Printf("Decremented concurrent requests for deployment: %s", deploymentName)
		}()

		// Wrap response writer to capture status code
		wrapped := &instrumentedResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // Default status
		}

		// Call the actual handler
		handler(wrapped, r)

		// Record total requests
		status := strconv.Itoa(wrapped.statusCode)
		totalRequestsCounter.WithLabelValues(deploymentName, endpoint, r.Method, status).Inc()
	}
}

// getDeploymentNameForMetrics returns deployment name for metrics labeling
func (app *AppState) getDeploymentNameForMetrics() string {
	app.mu.RLock()
	defer app.mu.RUnlock()

	if app.deploymentName != "" {
		return app.deploymentName
	}
	if app.Info.DeploymentName != "" {
		return app.Info.DeploymentName
	}
	return "unknown"
}

func (app *AppState) updateMetrics() {
	start := time.Now()
	defer func() {
		metricsCollectionDuration.Observe(time.Since(start).Seconds())
	}()

	d, n, err := internal.GetDeploymentInfo(app.clientset)
	if err != nil {
		log.Printf("Error updating deployment info: %v", err)
		metricsCollectionErrors.WithLabelValues("deployment_info").Inc()
	}

	cpu, mem, err := internal.GetPodMetrics(app.clientset, app.metricsClientset)
	reqs, reqErr := internal.GetConcurrentRequests(app.promAPI)

	app.mu.Lock()
	defer app.mu.Unlock()

	if err != nil {
		log.Printf("Error updating pod metrics: %v", err)
		metricsCollectionErrors.WithLabelValues("pod_metrics").Inc()
		app.Info.AverageCPU = "N/A"
		app.Info.AverageMemoryMiB = "N/A"
	} else {
		app.Info.AverageCPU = cpu
		app.Info.AverageMemoryMiB = mem
	}

	if reqErr != nil {
		log.Printf("Error updating http requests: %v", reqErr)
		metricsCollectionErrors.WithLabelValues("concurrent_requests").Inc()
		app.Info.ConcurrentRequests = -1 // Use -1 to indicate an error
	} else {
		app.Info.ConcurrentRequests = reqs
	}

	app.Info.DeploymentName = d
	app.Info.ReadyPods = n

	// Cache deployment name for metrics
	if d != "" {
		app.deploymentName = d
	}

	// Update Prometheus metrics
	app.updatePrometheusMetrics()

	log.Println("Metrics updated successfully.")
}

func (app *AppState) updatePrometheusMetrics() {
	deploymentName := app.Info.DeploymentName
	if deploymentName == "" {
		deploymentName = "unknown"
	}

	// Update ready pods gauge
	readyPodsGauge.WithLabelValues(deploymentName).Set(float64(app.Info.ReadyPods))

	// Update CPU gauge (convert from string if possible)
	if app.Info.AverageCPU != "N/A" && app.Info.AverageCPU != "" {
		if cpuValue, err := strconv.ParseFloat(app.Info.AverageCPU, 64); err == nil {
			avgCPUGauge.WithLabelValues(deploymentName).Set(cpuValue)
		}
	}

	// Update memory gauge (convert from string if possible)
	if app.Info.AverageMemoryMiB != "N/A" && app.Info.AverageMemoryMiB != "" {
		if memValue, err := strconv.ParseFloat(app.Info.AverageMemoryMiB, 64); err == nil {
			avgMemoryGauge.WithLabelValues(deploymentName).Set(memValue)
		}
	}

	// Update concurrent requests gauge (always set, use 0 for error state)
	// Note: This field is now kept for JSON API compatibility but not used for Prometheus
	if app.Info.ConcurrentRequests >= 0 {
		// Keep the JSON field updated for backward compatibility
	} else {
		// Keep error state for JSON API
	}
}

func (app *AppState) metricsHandler(w http.ResponseWriter, r *http.Request) {
	app.mu.RLock()
	defer app.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(app.Info); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// newPrometheusClient creates a client to connect to Prometheus.
func newPrometheusClient() (prometheusv1.API, error) {
	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		// This is a common default for the Prometheus service installed by the Prometheus Operator.
		promURL = "http://prometheus-k8s.monitoring.svc.cluster.local:9090"
		log.Printf("PROMETHEUS_URL not set, using default: %s", promURL)
	}

	client, err := api.NewClient(api.Config{
		Address: promURL,
	})
	if err != nil {
		return nil, fmt.Errorf("error creating Prometheus client: %w", err)
	}
	return prometheusv1.NewAPI(client), nil
}

func main() {
	intervalStr := os.Getenv("COLLECTION_INTERVAL_SECONDS")
	if intervalStr == "" {
		intervalStr = "15"
	}
	interval, err := strconv.Atoi(intervalStr)
	if err != nil {
		log.Fatalf("Invalid COLLECTION_INTERVAL_SECONDS: %v", err)
	}

	clientset, metricsClientset, err := internal.NewKubeClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes clients: %v", err)
	}

	// Initialize the Prometheus client
	promAPI, err := newPrometheusClient()
	if err != nil {
		log.Fatalf("Failed to create Prometheus client: %v", err)
	}

	app := &AppState{
		clientset:        clientset,
		metricsClientset: metricsClientset,
		promAPI:          promAPI, // Add client to state
	}

	log.Println("Performing initial metrics collection...")
	app.updateMetrics()

	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			log.Println("Ticker ticked. Collecting new metrics...")
			app.updateMetrics()
		}
	}()

	// Register handlers with instrumentation
	http.HandleFunc("/", app.httpInstrumentationMiddleware("root", app.metricsHandler))
	http.Handle("/metrics", promhttp.Handler())

	port := "8080"
	log.Printf("Starting server on port %s", port)
	log.Printf("JSON metrics available at: http://localhost:%s/", port)
	log.Printf("Prometheus metrics available at: http://localhost:%s/metrics", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
