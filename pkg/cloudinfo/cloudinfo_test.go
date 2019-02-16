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
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/banzaicloud/cloudinfo/internal/app/cloudinfo/tracing"
	"github.com/banzaicloud/cloudinfo/pkg/cloudinfo/metrics"
	"github.com/goph/logur"
	"github.com/stretchr/testify/assert"
)

// DummyCloudInfoer type implements the CloudInfoer interface for mockig of external calls
// the struct is to be extended according to the needs of test cases
type DummyCloudInfoer struct {
	AttrValues AttrValues
	TcId       string
	//dummyNetworkMapper NetworkPerfMapper
	// implement the interface
	CloudInfoer
}

func newDummyNetworkMapper() dummyNetworkMapper {
	return dummyNetworkMapper{}
}

type dummyNetworkMapper struct {
}

func (nm *dummyNetworkMapper) MapNetworkPerf(ntwPerf string) (string, error) {
	return "high", nil
}

const (
	GetRegionsError       = "could not get regions"
	GetCurrentPricesError = "could not get current prices"
	GetProductsError      = "could not get products"
	InitializeError       = "initialization failed"
	GetZonesError         = "could not get zones"
)

func (dpi *DummyCloudInfoer) Initialize() (map[string]map[string]Price, error) {
	switch dpi.TcId {
	case InitializeError:
		return nil, errors.New(InitializeError)
	default:
		return map[string]map[string]Price{
			"c1.xlarge":  {"dummy": {OnDemandPrice: 0.52, SpotPrice: SpotPriceInfo{"dummyZone1": 0.164}}},
			"c4.2xlarge": {"dummy": {OnDemandPrice: 0.4, SpotPrice: SpotPriceInfo{"dummyZone2": 0.12}}},
			"c3.large":   {"dummy": {OnDemandPrice: 0.11, SpotPrice: SpotPriceInfo{"dummyZone1": 0.053}}},
		}, nil
	}
}

func (dpi *DummyCloudInfoer) GetVirtualMachines(region string) ([]VmInfo, error) {
	return nil, nil
}

func (dpi *DummyCloudInfoer) GetProducts(vms []VmInfo, service, regionId string) ([]VmInfo, error) {
	switch dpi.TcId {
	case GetProductsError:
		return nil, errors.New(GetProductsError)
	default:
		return []VmInfo{
			{Cpus: float64(2),
				Mem:           float64(32),
				OnDemandPrice: float64(0.32)},
		}, nil
	}
}

func (dpi *DummyCloudInfoer) GetZones(region string) ([]string, error) {
	switch dpi.TcId {
	case GetZonesError:
		return nil, errors.New(GetZonesError)
	default:
		return []string{"dummyZone1", "dummyZone2"}, nil
	}
}

func (dpi *DummyCloudInfoer) GetRegion(id string) *endpoints.Region {
	return nil
}

func (dpi *DummyCloudInfoer) GetRegions(service string) (map[string]string, error) {
	switch dpi.TcId {
	case GetRegionsError:
		return nil, errors.New(GetRegionsError)
	default:
		return map[string]string{
			"EU (Frankfurt)":   "eu-central-1",
			"EU (Ireland)":     "eu-west-1",
			"US West (Oregon)": "us-west-2",
		}, nil
	}
}

func (dpi *DummyCloudInfoer) HasShortLivedPriceInfo() bool {
	return true
}

func (dpi *DummyCloudInfoer) GetCurrentPrices(region string) (map[string]Price, error) {
	switch dpi.TcId {
	case GetCurrentPricesError:
		return nil, errors.New(GetCurrentPricesError)
	default:
		return map[string]Price{
			"c1.xlarge":  {OnDemandPrice: 0.52, SpotPrice: SpotPriceInfo{"dummyZone1": 0.164}},
			"c4.2xlarge": {OnDemandPrice: 0.4, SpotPrice: SpotPriceInfo{"dummyZone2": 0.12}},
			"c3.large":   {OnDemandPrice: 0.11, SpotPrice: SpotPriceInfo{"dummyZone1": 0.053}},
		}, nil
	}

}

func (dpi *DummyCloudInfoer) GetNetworkPerformanceMapper() (NetworkPerfMapper, error) {
	nm := newDummyNetworkMapper()
	return &nm, nil
}

func (dpi *DummyCloudInfoer) MapNetworkPerf(vm VmInfo) (string, error) {
	return "high", nil
}

func TestNewCachingCloudInfo(t *testing.T) {
	tests := []struct {
		Name        string
		CloudInfoer map[string]CloudInfoer
		checker     func(info *cachingCloudInfo, err error)
	}{
		{
			Name: "product info successfully created",
			CloudInfoer: map[string]CloudInfoer{
				"dummy": &DummyCloudInfoer{},
			},
			checker: func(info *cachingCloudInfo, err error) {
				assert.Nil(t, err, "should not get error")
				assert.NotNil(t, info, "the product info should not be nil")
			},
		},
		{
			Name:        "validation should fail nil values",
			CloudInfoer: nil,
			checker: func(info *cachingCloudInfo, err error) {
				assert.Nil(t, info, "the cloudinfo should be nil in case of error")
				assert.EqualError(t, err, "could not create product infoer")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			test.checker(NewCachingCloudInfo(NewCacheProductStore(10*time.Minute, 5*time.Minute, logur.NewTestLogger()), test.CloudInfoer, metrics.NewNoOpMetricsReporter(), tracing.NewNoOpTracer()))
		})
	}

}

func TestCachingCloudInfo_GetRegions(t *testing.T) {
	tests := []struct {
		name        string
		CloudInfoer map[string]CloudInfoer
		checker     func(regions map[string]string, err error)
	}{
		{
			name: "successfully retrieved the regions",
			CloudInfoer: map[string]CloudInfoer{
				"dummy": &DummyCloudInfoer{},
			},
			checker: func(regions map[string]string, err error) {
				assert.Equal(t, map[string]string(map[string]string{"US West (Oregon)": "us-west-2", "EU (Frankfurt)": "eu-central-1", "EU (Ireland)": "eu-west-1"}), regions)
				assert.Equal(t, 3, len(regions))
				assert.Nil(t, err, "the error should be nil")
			},
		},
		{
			name: "could not retrieve regions",
			CloudInfoer: map[string]CloudInfoer{
				"dummy": &DummyCloudInfoer{TcId: GetRegionsError},
			},
			checker: func(regions map[string]string, err error) {
				assert.Nil(t, regions, "the error should be nil")
				assert.EqualError(t, err, GetRegionsError)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info, _ := NewCachingCloudInfo(NewCacheProductStore(5*time.Minute, 10*time.Minute, logur.NewTestLogger()), test.CloudInfoer, metrics.NewNoOpMetricsReporter(), tracing.NewNoOpTracer())
			test.checker(info.GetRegions(context.Background(), "dummy", "compute"))
		})
	}
}
