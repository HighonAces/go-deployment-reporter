package internal

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metrics "k8s.io/metrics/pkg/client/clientset/versioned"
	"path/filepath"
)

func NewKubeClient() (*kubernetes.Clientset, *metrics.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig, _ := filepath.Abs("/Users/srujanreddy/Downloads/dev-md-us-chi1_kubeconfig.yaml")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, nil, err
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}
	metricsClientset, err := metrics.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}
	return clientset, metricsClientset, nil
}
