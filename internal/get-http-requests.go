package internal

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

// GetConcurrentRequests queries Prometheus for the number of in-flight requests.
func GetConcurrentRequests(promAPI prometheusv1.API) (int64, error) {
	deploymentName := os.Getenv("DEPLOYMENT_NAME")
	if deploymentName == "" {
		return 0, fmt.Errorf("DEPLOYMENT_NAME env var must be set")
	}

	// This PromQL query sums the gauge values across all pods for the deployment.
	// IMPORTANT: This assumes your Prometheus is configured to add a 'deployment' label
	// to the metrics it scrapes, which is common when using the Prometheus Operator.
	// You may need to adjust the label (e.g., to 'app' or 'k8s_app').
	query := fmt.Sprintf(`sum(http_in_flight_requests{deployment="%s"})`, deploymentName)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, warnings, err := promAPI.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("error querying Prometheus: %w", err)
	}
	if len(warnings) > 0 {
		log.Printf("Prometheus warnings: %v", warnings)
	}

	// The result should be a vector (a list of time series).
	vec, ok := result.(model.Vector)
	if !ok {
		return 0, fmt.Errorf("unexpected result type: %T, expected model.Vector", result)
	}

	// If the vector is empty, it means no metrics were found, so we have 0 requests.
	if vec.Len() == 0 {
		return 0, nil
	}

	// Extract the numeric value from the first (and only) series in the vector.
	value := vec[0].Value
	return int64(value), nil
}
