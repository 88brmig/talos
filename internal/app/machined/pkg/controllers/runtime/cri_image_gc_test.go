// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package runtime_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/containerd/containerd/images"
	"github.com/siderolabs/gen/slices"
	"github.com/siderolabs/go-retry/retry"
	"github.com/stretchr/testify/suite"

	"github.com/siderolabs/talos/internal/app/machined/pkg/controllers/ctest"
	runtimectrl "github.com/siderolabs/talos/internal/app/machined/pkg/controllers/runtime"
	"github.com/siderolabs/talos/pkg/machinery/resources/etcd"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/v1alpha1"
)

func TestCRIImageGC(t *testing.T) {
	mockImageService := &mockImageService{}
	fakeClock := clock.NewMock()

	suite.Run(t, &CRIImageGCSuite{
		mockImageService: mockImageService,
		fakeClock:        fakeClock,
		DefaultSuite: ctest.DefaultSuite{
			AfterSetup: func(suite *ctest.DefaultSuite) {
				suite.Require().NoError(suite.Runtime().RegisterController(&runtimectrl.CRIImageGCController{
					ImageServiceProvider: func() (runtimectrl.ImageServiceProvider, error) {
						return mockImageService, nil
					},
					Clock: fakeClock,
				}))
			},
		},
	})
}

type mockImageService struct {
	mu sync.Mutex

	images []images.Image
}

func (m *mockImageService) ImageService() images.Store {
	return m
}

func (m *mockImageService) Close() error {
	return nil
}

func (m *mockImageService) Get(ctx context.Context, name string) (images.Image, error) {
	panic("not implemented")
}

func (m *mockImageService) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.images), nil
}

func (m *mockImageService) Create(ctx context.Context, image images.Image) (images.Image, error) {
	panic("not implemented")
}

func (m *mockImageService) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {
	panic("not implemented")
}

func (m *mockImageService) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.images = slices.FilterInPlace(m.images, func(i images.Image) bool { return i.Name != name })

	return nil
}

type CRIImageGCSuite struct {
	ctest.DefaultSuite

	mockImageService *mockImageService
	fakeClock        *clock.Mock
}

func (suite *CRIImageGCSuite) TestReconcile() {
	storedImages := []images.Image{
		{
			Name:      "registry.io/org/image1:v1.3.5@sha256:6b094bd0b063a1172eec7da249eccbb48cc48333800569363d67c747960cfa0a",
			CreatedAt: suite.fakeClock.Now().Add(-2 * runtimectrl.ImageGCGracePeriod),
		}, // ok to be gc'd
		{
			Name:      "registry.io/org/image1:v1.3.7",
			CreatedAt: suite.fakeClock.Now().Add(-2 * runtimectrl.ImageGCGracePeriod),
		}, // current image
		{
			Name:      "registry.io/org/image1:v1.3.8",
			CreatedAt: suite.fakeClock.Now(),
		}, // not ok to clean up, too new
		{
			Name:      "registry.io/org/image2@sha256:2f794176e9bd8a28501fa185693dc1073013a048c51585022ebce4f84b469db8",
			CreatedAt: suite.fakeClock.Now().Add(-2 * runtimectrl.ImageGCGracePeriod),
		}, // current image
	}

	suite.mockImageService.images = storedImages

	criService := v1alpha1.NewService("cri")
	criService.TypedSpec().Healthy = true
	criService.TypedSpec().Running = true

	suite.Require().NoError(suite.State().Create(suite.Ctx(), criService))

	kubelet := k8s.NewKubeletSpec(k8s.NamespaceName, k8s.KubeletID)
	kubelet.TypedSpec().Image = "registry.io/org/image1:v1.3.7"
	suite.Require().NoError(suite.State().Create(suite.Ctx(), kubelet))

	etcd := etcd.NewSpec(etcd.NamespaceName, etcd.SpecID)
	etcd.TypedSpec().Image = "registry.io/org/image2@sha256:2f794176e9bd8a28501fa185693dc1073013a048c51585022ebce4f84b469db8"
	suite.Require().NoError(suite.State().Create(suite.Ctx(), etcd))

	expectedImages := slices.Map(storedImages[1:4], func(i images.Image) string { return i.Name })

	suite.Assert().NoError(retry.Constant(5*time.Second, retry.WithUnits(100*time.Millisecond)).Retry(func() error {
		suite.fakeClock.Add(runtimectrl.ImageCleanupInterval)

		imageList, _ := suite.mockImageService.List(suite.Ctx()) //nolint:errcheck
		actualImages := slices.Map(imageList, func(i images.Image) string { return i.Name })

		if reflect.DeepEqual(expectedImages, actualImages) {
			return nil
		}

		return retry.ExpectedErrorf("images don't match: expected %v actual %v", expectedImages, actualImages)
	}))
}
