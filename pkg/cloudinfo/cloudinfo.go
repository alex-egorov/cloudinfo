// Copyright © 2018 Banzai Cloud
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cloudinfo

import (
	"context"
	"github.com/banzaicloud/cloudinfo/internal/app/cloudinfo/tracing"
	"github.com/banzaicloud/cloudinfo/pkg/cloudinfo/metrics"
	"github.com/banzaicloud/cloudinfo/pkg/logger"
	"github.com/goph/emperror"
	"github.com/pkg/errors"
	"strings"
)

// cachingCloudInfo is the module struct, holds configuration and cache
// It's the entry point for the product info retrieval and management subsystem
// It's also responsible for delegating to the cloud provider specific implementations
type cachingCloudInfo struct {
	cloudInfoers   map[string]CloudInfoer
	cloudInfoStore CloudInfoStore
	metrics        metrics.Reporter
	tracer         tracing.Tracer
}

func (v AttrValues) floatValues() []float64 {
	floatValues := make([]float64, len(v))
	for i, av := range v {
		floatValues[i] = av.Value
	}
	return floatValues
}

// SpotPriceInfo represents different prices per availability zones
type SpotPriceInfo map[string]float64

// Price describes the on demand price and spot prices per availability zones
type Price struct {
	OnDemandPrice float64       `json:"onDemandPrice"`
	SpotPrice     SpotPriceInfo `json:"spotPrice"`
}

// VmInfo representation of a virtual machine
type VmInfo struct {
	Type          string            `json:"type"`
	OnDemandPrice float64           `json:"onDemandPrice"`
	SpotPrice     SpotPriceInfo     `json:"spotPrice"`
	Cpus          float64           `json:"cpusPerVm"`
	Mem           float64           `json:"memPerVm"`
	Gpus          float64           `json:"gpusPerVm"`
	NtwPerf       string            `json:"ntwPerf"`
	NtwPerfCat    string            `json:"ntwPerfCategory"`
	Zones         []string          `json:"zones"`
	Attributes    map[string]string `json:"attributes"`
	// CurrentGen signals whether the instance type generation is the current one. Only applies for amazon
	CurrentGen bool `json:"currentGen"`
}

// IsBurst returns true if the EC2 instance vCPU is burst type
// the decision is made based on the instance type
func (vm VmInfo) IsBurst() bool {
	return strings.HasPrefix(strings.ToUpper(vm.Type), "T")
}

// NewCachingCloudInfo creates a new cachingCloudInfo instance
func NewCachingCloudInfo(ciStore CloudInfoStore, infoers map[string]CloudInfoer, reporter metrics.Reporter, tracer tracing.Tracer) (*cachingCloudInfo, error) {
	if infoers == nil || ciStore == nil {
		return nil, errors.New("could not create product infoer")
	}

	pi := cachingCloudInfo{
		cloudInfoers:   infoers,
		cloudInfoStore: ciStore,
		metrics:        reporter,
		tracer:         tracer,
	}
	return &pi, nil
}

// GetProviders returns the supported providers
func (cpi *cachingCloudInfo) GetProviders(ctx context.Context) []Provider {
	var (
		providers []Provider
		provider  Provider
		err       error
	)
	log := logger.Extract(ctx)

	// iterate over supported provider names only
	for pn := range cpi.cloudInfoers {
		if provider, err = cpi.GetProvider(ctx, pn); err != nil {
			log.Warn("could not retrieve provider", map[string]interface{}{"provider": provider})
		}

		providers = append(providers, provider)
	}

	return providers
}

// GetProvider returns the supported provider
func (cpi *cachingCloudInfo) GetProvider(ctx context.Context, provider string) (Provider, error) {
	var (
		srvcs []Service
		err   error
	)

	if _, ok := cpi.cloudInfoers[provider]; !ok {
		return Provider{}, emperror.With(errors.New("unsupported provider"), "provider", provider)
	}

	if srvcs, err = cpi.GetServices(ctx, provider); err != nil {
		return Provider{}, emperror.With(errors.New("no supported services for provider"), "provider", provider)
	}

	// decorate the provider with service information
	p := NewProvider(provider)
	p.Services = srvcs

	return p, nil
}

// GetAttributes returns the supported attribute names
func (cpi *cachingCloudInfo) GetAttributes(ctx context.Context) []string {
	return []string{Cpu, Memory}
}

// GetAttrValues returns a slice with the values for the given attribute name
func (cpi *cachingCloudInfo) GetAttrValues(ctx context.Context, provider, service, attribute string) ([]float64, error) {
	if cachedVal, ok := cpi.cloudInfoStore.GetAttribute(provider, service, attribute); ok {
		return cachedVal.(AttrValues).floatValues(), nil
	}

	return nil, emperror.With(errors.New("attributes not yet cached"),
		"provider", provider, "service", service, "attribute", attribute)
}

// GetZones returns the availability zones in a region
func (cpi *cachingCloudInfo) GetZones(ctx context.Context, provider string, region string) ([]string, error) {
	if cachedVal, ok := cpi.cloudInfoStore.GetZones(provider, region); ok {
		return cachedVal.([]string), nil
	}

	return nil, emperror.With(errors.New("zones not yet cached"), "provider", provider, "region", region)
}

// GetRegions gets the regions for the provided provider
func (cpi *cachingCloudInfo) GetRegions(ctx context.Context, provider, service string) (map[string]string, error) {
	if cachedVal, ok := cpi.cloudInfoStore.GetRegions(provider, service); ok {
		return cachedVal.(map[string]string), nil
	}

	return nil, emperror.With(errors.New("regions not yet cached"), "provider", provider, "services", service)
}

func (cpi *cachingCloudInfo) GetServices(ctx context.Context, provider string) ([]Service, error) {
	if cachedVal, ok := cpi.cloudInfoStore.GetServices(provider); ok {
		return cachedVal.([]Service), nil
	}

	return nil, emperror.With(errors.New("services not yet cached"), "provider", provider)
}

// GetProductDetails retrieves product details form the given provider and region
func (cpi *cachingCloudInfo) GetProductDetails(ctx context.Context, provider, service, region string) ([]ProductDetails, error) {
	var (
		vms interface{}
		ok  bool
	)
	log := logger.Extract(ctx)
	log.Info("retrieving product details")
	if vms, ok = cpi.cloudInfoStore.GetVm(provider, service, region); !ok {
		return nil, emperror.With(errors.New("vms not yet cached"),
			"provider", provider, "service", service, "region", region)
	}

	var details []ProductDetails

	var pr Price
	for _, vm := range vms.([]VmInfo) {
		pd := newProductDetails(vm)
		if cachedVal, ok := cpi.cloudInfoStore.GetPrice(provider, region, vm.Type); ok {
			pr = cachedVal.(Price)
			for zone, price := range pr.SpotPrice {
				pd.SpotInfo = append(pd.SpotInfo, *newZonePrice(zone, price))
			}
		} else {
			log.Debug("price info not yet cached", map[string]interface{}{"instanceType": vm.Type})
		}

		details = append(details, *pd)
	}

	return details, nil
}

// GetStatus retrieves status form the given provider
func (cpi *cachingCloudInfo) GetStatus(provider string) (string, error) {
	if cachedStatus, ok := cpi.cloudInfoStore.GetStatus(provider); ok {
		return cachedStatus.(string), nil
	}
	return "", emperror.With(errors.New("status not yet cached"), "provider", provider)
}

// GetServiceImages retrieves available images for the given provider, service and region
func (cpi *cachingCloudInfo) GetServiceImages(ctx context.Context, provider, service, region string) ([]Image, error) {
	logger.Extract(ctx).Debug("getting available images")

	if cachedImages, ok := cpi.cloudInfoStore.GetImage(provider, service, region); ok {
		return cachedImages.([]Image), nil
	}

	return nil, emperror.With(errors.New("images not yet cached"), "provider", provider,
		"service", service, "region", region)

}

// GetVersions retrieves available versions for the given provider, service and region
func (cpi *cachingCloudInfo) GetVersions(ctx context.Context, provider, service, region string) ([]string, error) {
	logger.Extract(ctx).Debug("getting available versions")

	if cachedVersions, ok := cpi.cloudInfoStore.GetVersion(provider, service, region); ok {
		return cachedVersions.([]string), nil
	}
	return nil, emperror.With(errors.New("versions not yet cached"),
		"provider", provider, "service", service, "region", region)

}

// Contains is a helper function to check if a slice contains a string
func Contains(slice []string, s string) bool {
	for _, e := range slice {
		if e == s {
			return true
		}
	}
	return false
}

// Attributes create a map with the specified parameters
func Attributes(cpu, memory, ntwPerfCat string) map[string]string {
	var attributes = make(map[string]string)

	attributes[Cpu] = cpu
	attributes[Memory] = memory
	attributes["NetworkPerfCategory"] = ntwPerfCat

	return attributes
}

// CreateString builds strings effectively
func CreateString(keys ...string) string {
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
	}
	return b.String()
}
