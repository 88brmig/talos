// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package runtime_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/rtestutils"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/registry"
	"github.com/siderolabs/go-retry/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/siderolabs/talos/internal/app/machined/pkg/controllers/ctest"
	runtimectrl "github.com/siderolabs/talos/internal/app/machined/pkg/controllers/runtime"
	"github.com/siderolabs/talos/internal/app/machined/pkg/controllers/secrets"
	talosruntime "github.com/siderolabs/talos/internal/app/machined/pkg/runtime"
	"github.com/siderolabs/talos/internal/app/maintenance"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

func TestMaintenanceServiceSuite(t *testing.T) {
	suite.Run(t, &MaintenanceServiceSuite{
		DefaultSuite: ctest.DefaultSuite{
			Timeout: 5 * time.Second,
			AfterSetup: func(suite *ctest.DefaultSuite) {
				maintenance.InjectController(mockController{s: suite.State()})

				suite.Require().NoError(suite.Runtime().RegisterController(&secrets.MaintenanceRootController{}))
				suite.Require().NoError(suite.Runtime().RegisterController(&secrets.MaintenanceCertSANsController{}))
				suite.Require().NoError(suite.Runtime().RegisterController(&secrets.MaintenanceController{}))
				suite.Require().NoError(suite.Runtime().RegisterController(&runtimectrl.MaintenanceServiceController{}))
			},
		},
	})
}

type MaintenanceServiceSuite struct {
	ctest.DefaultSuite
}

func (suite *MaintenanceServiceSuite) findListenAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	suite.Require().NoError(err)

	addr := l.Addr().String()

	suite.Require().NoError(l.Close())

	return addr
}

func (suite *MaintenanceServiceSuite) TestRunService() {
	nodeAddresses := network.NewNodeAddress(network.NamespaceName, network.NodeAddressAccumulativeID)
	nodeAddresses.TypedSpec().Addresses = []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")}
	suite.Require().NoError(suite.State().Create(suite.Ctx(), nodeAddresses))

	maintenanceConfig := runtime.NewMaintenanceServiceConfig()
	maintenanceConfig.TypedSpec().ListenAddress = suite.findListenAddr()
	maintenanceConfig.TypedSpec().ReachableAddresses = []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	suite.Require().NoError(suite.State().Create(suite.Ctx(), maintenanceConfig))

	maintenanceRequest := runtime.NewMaintenanceServiceRequest()
	suite.Require().NoError(suite.State().Create(suite.Ctx(), maintenanceRequest))

	// wait for the service to be up
	suite.AssertWithin(time.Second, 10*time.Millisecond, func() error {
		c, err := tls.Dial("tcp", maintenanceConfig.TypedSpec().ListenAddress,
			&tls.Config{
				InsecureSkipVerify: true,
			},
		)

		if c != nil {
			c.Close() //nolint:errcheck
		}

		return retry.ExpectedError(err)
	})

	// test API
	mc, err := client.New(suite.Ctx(),
		client.WithTLSConfig(&tls.Config{
			InsecureSkipVerify: true,
		}), client.WithEndpoints(maintenanceConfig.TypedSpec().ListenAddress),
	)
	suite.Require().NoError(err)

	_, err = mc.Version(suite.Ctx())
	suite.Require().ErrorContains(err, "API is not implemented in maintenance mode")

	suite.Require().NoError(mc.Close())

	// teardown the maintenance service
	_, err = suite.State().Teardown(suite.Ctx(), maintenanceRequest.Metadata())
	suite.Require().NoError(err)

	rtestutils.AssertResources(suite.Ctx(), suite.T(), suite.State(), []resource.ID{runtime.MaintenanceServiceRequestID},
		func(r *runtime.MaintenanceServiceRequest, asrt *assert.Assertions) {
			asrt.Empty(r.Metadata().Finalizers())
		})

	suite.Require().NoError(suite.State().Destroy(suite.Ctx(), maintenanceRequest.Metadata()))

	_, err = net.Dial("tcp", maintenanceConfig.TypedSpec().ListenAddress)
	suite.Require().ErrorContains(err, "connection refused")
}

type mockController struct {
	s state.State
}

type mockState struct {
	s state.State
}

func (mock mockController) Runtime() talosruntime.Runtime {
	return mock
}

func (mockController) Sequencer() talosruntime.Sequencer {
	return nil
}

func (mockController) Run(context.Context, talosruntime.Sequence, any, ...talosruntime.LockOption) error {
	return nil
}

func (mockController) V1Alpha2() talosruntime.V1Alpha2Controller {
	return nil
}

func (mock mockController) Config() config.Config {
	return nil
}

func (mock mockController) ConfigContainer() config.Container {
	return nil
}

func (mock mockController) RollbackToConfigAfter(time.Duration) error {
	return nil
}

func (mock mockController) CancelConfigRollbackTimeout() {
}

func (mock mockController) SetConfig(config.Provider) error {
	return nil
}

func (mock mockController) CanApplyImmediate(config.Provider) error {
	return nil
}

func (mock mockController) GetSystemInformation(context.Context) (*hardware.SystemInformation, error) {
	return nil, nil
}

func (mock mockController) State() talosruntime.State {
	return mockState(mock)
}

func (mock mockController) Events() talosruntime.EventStream {
	return nil
}

func (mock mockController) Logging() talosruntime.LoggingManager {
	return nil
}

func (mock mockController) NodeName() (string, error) {
	return "", nil
}

func (mock mockController) IsBootstrapAllowed() bool {
	return false
}

func (mock mockState) Platform() talosruntime.Platform {
	return nil
}

func (mock mockState) Machine() talosruntime.MachineState {
	return nil
}

func (mock mockState) Cluster() talosruntime.ClusterState {
	return nil
}

func (mock mockState) V1Alpha2() talosruntime.V1Alpha2State {
	return mock
}

func (mock mockState) Resources() state.State {
	return mock.s
}

func (mock mockState) NamespaceRegistry() *registry.NamespaceRegistry {
	return nil
}

func (mock mockState) ResourceRegistry() *registry.ResourceRegistry {
	return nil
}

func (mock mockState) SetConfig(config.Provider) error {
	return nil
}
