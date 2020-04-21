package main

import (
	"context"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

var (
	timeoutDail      = 10 * time.Second
	envAllocatedKey  = "NOOP_DEVICES"
	deviceSocketPath = "noop.sock"
	resourceName     = "fuweid.com/noop"
)

func newNoopDevicePlugin() *noopDevicePlugin {
	return &noopDevicePlugin{
		resourceName: resourceName,
		socket:       filepath.Join(pluginapi.DevicePluginPath, deviceSocketPath),
		server:       grpc.NewServer(),
		closeCh:      make(chan struct{}),
	}
}

// noopDevicePlugin only returns env for container, not device and mounts.
//
// NOTE: Device is always healthy and no need to check.
type noopDevicePlugin struct {
	resourceName string
	socket       string
	server       *grpc.Server
	deviceIDs    []string
	closeCh      chan struct{}
}

func (plugin *noopDevicePlugin) start() error {
	pluginapi.RegisterDevicePluginServer(plugin.server, plugin)

	plugin.deviceIDs = []string{"1", "2", "3"}

	// cleanup socket
	os.RemoveAll(plugin.socket)

	lis, err := net.Listen("unix", plugin.socket)
	if err != nil {
		return err
	}
	defer lis.Close()

	return plugin.server.Serve(lis)
}

func (plugin *noopDevicePlugin) stop() error {
	plugin.server.GracefulStop()
	close(plugin.closeCh)
	return nil
}

// Register pings kubelet know noopDevicePlugin is here.
func (plugin *noopDevicePlugin) Register() error {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutDail)
	defer cancel()

	conn, err := dial(ctx, pluginapi.KubeletSocket)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)

	req := &pluginapi.RegisterRequest{
		ResourceName: plugin.resourceName,
		Endpoint:     path.Base(plugin.socket),
		Version:      pluginapi.Version,
	}

	_, err = client.Register(context.Background(), req)
	return errors.Wrapf(err, "failed to register into kubelet")
}

// GetDevicePluginOptions returns options to be communicated with Device Manager
func (plugin *noopDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	logrus.Info("GetDevicePluginOptions request")
	return &pluginapi.DevicePluginOptions{}, nil
}

// ListAndWatch returns a stream of List of Devices
// Whenever a Device state change or a Device disappears, ListAndWatch
// returns the new list
//
// NOTE: No real device here and no device will be changed.
func (plugin *noopDevicePlugin) ListAndWatch(_ *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	s.Send(&pluginapi.ListAndWatchResponse{
		Devices: plugin.deviceList(),
	})

	select {
	case <-plugin.closeCh:
		return nil
	}
}

// Allocate is called during container creation so that the Device
// Plugin can run device specific operations and instruct Kubelet
// of the steps to make the Device available in the container
func (plugin *noopDevicePlugin) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	logrus.Infof("Allocate request: %v", *req)
	resp := pluginapi.AllocateResponse{}

	for _, r := range req.ContainerRequests {
		for _, id := range r.DevicesIDs {
			if !plugin.hasDeviceID(id) {
				return nil, errors.Errorf("invalid allocation request for %s about unknown device: %s", plugin.resourceName, id)
			}
		}

		resp.ContainerResponses = append(resp.ContainerResponses, &pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{
				envAllocatedKey: strings.Join(r.DevicesIDs, ","),
			},
		})
	}

	return &resp, nil
}

// PreStartContainer is called, if indicated by Device Plugin during registeration phase,
// before each container start. Device plugin can run device specific operations
// such as resetting the device before making devices available to the container
func (plugin *noopDevicePlugin) PreStartContainer(_ context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	logrus.Infof("PreStartContainer request: %v", *req)
	return &pluginapi.PreStartContainerResponse{}, nil
}

func (plugin *noopDevicePlugin) deviceList() []*pluginapi.Device {
	res := make([]*pluginapi.Device, 0, len(plugin.deviceIDs))
	for _, d := range plugin.deviceIDs {
		res = append(res, &pluginapi.Device{
			ID:     d,
			Health: pluginapi.Healthy,
		})
	}
	return res
}

func (plugin *noopDevicePlugin) hasDeviceID(id string) bool {
	for _, i := range plugin.deviceIDs {
		if i == id {
			return true
		}
	}
	return false
}

func dial(ctx context.Context, socket string) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	}

	return grpc.DialContext(ctx, socket, opts...)
}
