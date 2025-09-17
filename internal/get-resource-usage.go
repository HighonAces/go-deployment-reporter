package internal

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
)

func GetPodMetrics(clientset *kubernetes.Clientset, metricsClient *metrics.Clientset) (cpu string, memory string, err error) {
	// ... (code to get deployment and selector remains the same) ...
	deploymentName := os.Getenv("DEPLOYMENT_NAME")
	namespace := os.Getenv("POD_NAMESPACE")
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(context.TODO(), deploymentName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to get deployment: %w", err)
	}

	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return "", "", fmt.Errorf("failed to create label selector: %w", err)
	}

	podMetricsList, err := metricsClient.MetricsV1beta1().PodMetricses(namespace).List(context.TODO(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to list pod metrics: %w", err)
	}

	totalCPU := resource.NewQuantity(0, resource.DecimalSI)
	totalMemory := resource.NewQuantity(0, resource.BinarySI) // Correct unit for memory
	containerCount := 0

	for _, podMetrics := range podMetricsList.Items {
		for _, container := range podMetrics.Containers {
			totalCPU.Add(*container.Usage.Cpu())
			totalMemory.Add(*container.Usage.Memory())
			containerCount++
		}
	}

	// --- IMPROVEMENT: Prevent division by zero ---
	if containerCount == 0 {
		return "0", "0", nil
	}

	// Calculate average CPU in milli-cores
	avgCPUMilli := totalCPU.MilliValue() / int64(containerCount)

	// Calculate average Memory in bytes, then convert to MiB
	avgMemoryBytes := totalMemory.Value() / int64(containerCount)
	avgMemoryMiB := avgMemoryBytes / (1024 * 1024)

	return strconv.FormatInt(avgCPUMilli, 10), strconv.FormatInt(avgMemoryMiB, 10), nil
}
