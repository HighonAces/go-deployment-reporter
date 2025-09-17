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

type AppState struct {
	mu               sync.RWMutex
	Info             Info
	clientset        *kubernetes.Clientset
	metricsClientset *metrics.Clientset
	promAPI          prometheusv1.API // New field
}

func (app *AppState) updateMetrics() {
	d, n, err := internal.GetDeploymentInfo(app.clientset)
	if err != nil {
		log.Printf("Error updating deployment info: %v", err)
	}

	cpu, mem, err := internal.GetPodMetrics(app.clientset, app.metricsClientset)
	reqs, reqErr := internal.GetConcurrentRequests(app.promAPI)

	app.mu.Lock()
	defer app.mu.Unlock()

	if err != nil {
		log.Printf("Error updating pod metrics: %v", err)
		app.Info.AverageCPU = "N/A"
		app.Info.AverageMemoryMiB = "N/A"
	} else {
		app.Info.AverageCPU = cpu
		app.Info.AverageMemoryMiB = mem
	}

	if reqErr != nil {
		log.Printf("Error updating http requests: %v", reqErr)
		app.Info.ConcurrentRequests = -1 // Use -1 to indicate an error
	} else {
		app.Info.ConcurrentRequests = reqs
	}

	app.Info.DeploymentName = d
	app.Info.ReadyPods = n

	log.Println("Metrics updated successfully.")
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

	http.HandleFunc("/", app.metricsHandler)
	port := "8080"
	log.Printf("Starting server on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
