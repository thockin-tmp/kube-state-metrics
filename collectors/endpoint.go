/*
Copyright 2017 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package collectors

import (
	"golang.org/x/net/context"

	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var (
	descEndpointLabelsName          = "kube_endpoint_labels"
	descEndpointLabelsHelp          = "Kubernetes labels converted to Prometheus labels."
	descEndpointLabelsDefaultLabels = []string{"namespace", "endpoint"}

	descEndpointInfo = prometheus.NewDesc(
		"kube_endpoint_info",
		"Information about endpoint.",
		[]string{"namespace", "endpoint"}, nil,
	)

	descEndpointCreated = prometheus.NewDesc(
		"kube_endpoint_created",
		"Unix creation timestamp",
		[]string{"namespace", "endpoint"}, nil,
	)

	descEndpointLabels = prometheus.NewDesc(
		descEndpointLabelsName,
		descEndpointLabelsHelp,
		descEndpointLabelsDefaultLabels, nil,
	)

	descEndpointAddressAvailable = prometheus.NewDesc(
		"kube_endpoint_address_available",
		"Number of addresses available in endpoint.",
		[]string{"namespace", "endpoint"}, nil)

	descEndpointAddressNotReady = prometheus.NewDesc(
		"kube_endpoint_address_not_ready",
		"Number of addresses not ready in endpoint",
		[]string{"namespace", "endpoint"}, nil)
)

type EndpointLister func() ([]v1.Endpoints, error)

func (l EndpointLister) List() ([]v1.Endpoints, error) {
	return l()
}

func RegisterEndpointCollector(registry prometheus.Registerer, kubeClient kubernetes.Interface, namespace string) {
	client := kubeClient.CoreV1().RESTClient()
	glog.Infof("collect endpoint with %s", client.APIVersion())
	slw := cache.NewListWatchFromClient(client, "endpoints", namespace, fields.Everything())
	sinf := cache.NewSharedInformer(slw, &v1.Endpoints{}, resyncPeriod)

	endpointLister := EndpointLister(func() (endpoints []v1.Endpoints, err error) {
		for _, m := range sinf.GetStore().List() {
			endpoints = append(endpoints, *m.(*v1.Endpoints))
		}
		return endpoints, nil
	})

	registry.MustRegister(&endpointCollector{store: endpointLister})
	go sinf.Run(context.Background().Done())
}

type endpointStore interface {
	List() (endpoints []v1.Endpoints, err error)
}

// endpointCollector collects metrics about all endpoints in the cluster.
type endpointCollector struct {
	store endpointStore
}

// Describe implements the prometheus.Collector interface.
func (pc *endpointCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descEndpointInfo
	ch <- descEndpointLabels
	ch <- descEndpointCreated
	ch <- descEndpointAddressAvailable
	ch <- descEndpointAddressNotReady
}

// Collect implements the prometheus.Collector interface.
func (ec *endpointCollector) Collect(ch chan<- prometheus.Metric) {
	endpoints, err := ec.store.List()
	if err != nil {
		glog.Errorf("listing endpoints failed: %s", err)
		return
	}
	for _, e := range endpoints {
		ec.collectEndpoints(ch, e)
	}

	glog.Infof("collected %d endpoints", len(endpoints))
}

func (ec *endpointCollector) collectEndpoints(ch chan<- prometheus.Metric, e v1.Endpoints) {
	addConstMetric := func(desc *prometheus.Desc, t prometheus.ValueType, v float64, lv ...string) {
		lv = append([]string{e.Namespace, e.Name}, lv...)
		ch <- prometheus.MustNewConstMetric(desc, t, v, lv...)
	}
	addGauge := func(desc *prometheus.Desc, v float64, lv ...string) {
		addConstMetric(desc, prometheus.GaugeValue, v, lv...)
	}

	addGauge(descEndpointInfo, 1)
	if !e.CreationTimestamp.IsZero() {
		addGauge(descEndpointCreated, float64(e.CreationTimestamp.Unix()))
	}
	labelKeys, labelValues := kubeLabelsToPrometheusLabels(e.Labels)
	addGauge(endpointLabelsDesc(labelKeys), 1, labelValues...)

	var available int
	for _, s := range e.Subsets {
		available += len(s.Addresses) * len(s.Ports)
	}
	addGauge(descEndpointAddressAvailable, float64(available))

	var notReady int
	for _, s := range e.Subsets {
		notReady += len(s.NotReadyAddresses) * len(s.Ports)
	}
	addGauge(descEndpointAddressNotReady, float64(notReady))
}

func endpointLabelsDesc(labelKeys []string) *prometheus.Desc {
	return prometheus.NewDesc(
		descEndpointLabelsName,
		descEndpointLabelsHelp,
		append(descEndpointLabelsDefaultLabels, labelKeys...),
		nil,
	)
}
