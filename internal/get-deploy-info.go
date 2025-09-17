package internal

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"os"
)

func GetDeploymentInfo(clientset *kubernetes.Clientset) (string, int32, error) {
	deploymentName := os.Getenv("DEPLOYMENT_NAME")
	namespace := os.Getenv("POD_NAMESPACE")

	if deploymentName == "" || namespace == "" {
		return "", 0, fmt.Errorf("DEPLOYMENT_NAME and POD_NAMESPACE env vars must be set")
	}
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(context.TODO(), deploymentName, metav1.GetOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("failed to get deployment: %w", err)
	}
	readyPods := deployment.Status.ReadyReplicas
	return deploymentName, readyPods, nil
}
