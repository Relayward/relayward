package pluginruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/manifest"

	"github.com/Relayward/relayward/internal/store"
)

const (
	pluginStartupTimeout = 15 * time.Second
	pluginRPCTimeout     = 15 * time.Second
	pluginHealthTimeout  = 30 * time.Second
	pluginStopTimeout    = 10 * time.Second
	maximumGRPCMessage   = 1 << 20
)

type managedProcess struct {
	command      *exec.Cmd
	client       centerpluginv1.CenterPluginClient
	connection   *grpc.ClientConn
	hostServer   *grpc.Server
	hostListener net.Listener
	pluginSocket string
	hostSocket   string
	done         chan struct{}
	waitError    error
	startedAt    time.Time
	closeOnce    sync.Once
}

func (supervisor *Supervisor) startProcess(ctx context.Context, version store.PluginVersion) (*managedProcess, error) {
	paths, err := supervisor.artifacts.Paths(version.PluginID, version.Version)
	if err != nil {
		return nil, err
	}
	if err := verifyCenterArtifact(paths.Executable, version.Manifest); err != nil {
		return nil, err
	}
	dataDirectory, err := supervisor.artifacts.DataDirectory(version.PluginID)
	if err != nil {
		return nil, err
	}
	runtimeDirectory, err := supervisor.artifacts.RuntimeDirectory(version.PluginID)
	if err != nil {
		return nil, err
	}
	pluginSocket := filepath.Join(runtimeDirectory, "plugin.sock")
	hostSocket := filepath.Join(runtimeDirectory, "host.sock")
	if len(pluginSocket) >= 108 || len(hostSocket) >= 108 {
		return nil, errors.New("center data directory is too long for plugin Unix sockets")
	}
	for _, socket := range []string{pluginSocket, hostSocket} {
		if err := removeStaleSocket(socket); err != nil {
			return nil, err
		}
	}
	hostListener, err := net.Listen("unix", hostSocket)
	if err != nil {
		return nil, fmt.Errorf("listen on center plugin Host socket: %w", err)
	}
	if err := os.Chmod(hostSocket, 0o600); err != nil {
		hostListener.Close()
		return nil, fmt.Errorf("protect center plugin Host socket: %w", err)
	}
	permissions := append([]string(nil), version.ApprovedPermissions...)
	sort.Strings(permissions)
	activation := &centerpluginv1.ActivateRequest{Permissions: permissions}
	if err := centerpluginv1.ValidateActivateRequest(activation); err != nil {
		hostListener.Close()
		return nil, fmt.Errorf("validate center plugin permissions: %w", err)
	}
	hostServer := grpc.NewServer(grpc.MaxRecvMsgSize(maximumGRPCMessage), grpc.MaxSendMsgSize(maximumGRPCMessage))
	centerpluginv1.RegisterPluginHostServer(hostServer, newHostService(
		supervisor.database, supervisor.events, supervisor.nodePlugins, version.PluginID, version.Version, permissions,
	))
	go func() { _ = hostServer.Serve(hostListener) }()

	launcher, err := os.Executable()
	if err != nil {
		hostServer.Stop()
		hostListener.Close()
		_ = os.Remove(hostSocket)
		return nil, fmt.Errorf("resolve center plugin launcher: %w", err)
	}
	command := exec.Command(launcher, "plugin-exec", paths.Executable)
	command.Dir = dataDirectory
	command.Env = []string{
		"HOME=" + dataDirectory,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TMPDIR=" + runtimeDirectory,
		centerpluginv1.EnvironmentPluginSocket + "=" + pluginSocket,
		centerpluginv1.EnvironmentHostSocket + "=" + hostSocket,
		centerpluginv1.EnvironmentDataDirectory + "=" + dataDirectory,
		centerpluginv1.EnvironmentPluginID + "=" + version.PluginID,
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if err := command.Start(); err != nil {
		hostServer.Stop()
		hostListener.Close()
		_ = os.Remove(hostSocket)
		return nil, fmt.Errorf("start center plugin process: %w", err)
	}
	process := &managedProcess{
		command: command, hostServer: hostServer, hostListener: hostListener,
		pluginSocket: pluginSocket, hostSocket: hostSocket, done: make(chan struct{}), startedAt: time.Now().UTC(),
	}
	go func() {
		process.waitError = command.Wait()
		close(process.done)
	}()
	startupContext, cancel := context.WithTimeout(ctx, pluginStartupTimeout)
	defer cancel()
	connection, client, err := connectPlugin(startupContext, process, version.PluginID, version.Version)
	if err != nil {
		_ = process.stop(context.Background())
		return nil, err
	}
	process.connection = connection
	process.client = client
	callContext, callCancel := context.WithTimeout(ctx, pluginRPCTimeout)
	activated, err := client.Activate(callContext, activation)
	callCancel()
	if err != nil {
		_ = process.stop(context.Background())
		return nil, errors.New("center plugin activation failed")
	}
	if err := centerpluginv1.ValidateActivated(activation, activated); err != nil {
		_ = process.stop(context.Background())
		return nil, fmt.Errorf("validate center plugin activation: %w", err)
	}
	healthContext, healthCancel := context.WithTimeout(ctx, pluginHealthTimeout)
	defer healthCancel()
	if err := waitHealthy(healthContext, process); err != nil {
		_ = process.stop(context.Background())
		return nil, err
	}
	return process, nil
}

func connectPlugin(ctx context.Context, process *managedProcess, pluginID, version string) (*grpc.ClientConn, centerpluginv1.CenterPluginClient, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(process.pluginSocket)
		if err == nil && info.Mode()&os.ModeSocket != 0 {
			connection, err := grpc.NewClient("passthrough:///center-plugin",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maximumGRPCMessage), grpc.MaxCallSendMsgSize(maximumGRPCMessage)),
				grpc.WithContextDialer(func(dialContext context.Context, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(dialContext, "unix", process.pluginSocket)
				}),
			)
			if err != nil {
				return nil, nil, fmt.Errorf("connect center plugin: %w", err)
			}
			client := centerpluginv1.NewCenterPluginClient(connection)
			callContext, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			response, callErr := client.GetInfo(callContext, &centerpluginv1.GetInfoRequest{})
			cancel()
			if callErr == nil {
				if err := centerpluginv1.ValidateInfoResponse(response, pluginID, version); err != nil {
					connection.Close()
					return nil, nil, fmt.Errorf("validate center plugin identity: %w", err)
				}
				return connection, client, nil
			}
			connection.Close()
		}
		select {
		case <-ctx.Done():
			return nil, nil, errors.New("center plugin did not become ready before the startup deadline")
		case <-process.done:
			return nil, nil, errors.New("center plugin exited before becoming ready")
		case <-ticker.C:
		}
	}
}

func waitHealthy(ctx context.Context, process *managedProcess) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		callContext, cancel := context.WithTimeout(ctx, pluginRPCTimeout)
		response, err := process.client.GetStatus(callContext, &centerpluginv1.GetStatusRequest{})
		cancel()
		if err == nil {
			if err := centerpluginv1.ValidateStatusResponse(response); err != nil {
				return fmt.Errorf("validate center plugin health: %w", err)
			}
			switch response.Health {
			case centerpluginv1.Health_HEALTH_HEALTHY:
				return nil
			case centerpluginv1.Health_HEALTH_UNHEALTHY:
				return errors.New("center plugin reported unhealthy during activation")
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("center plugin did not become healthy before the deadline")
		case <-process.done:
			return errors.New("center plugin exited before becoming healthy")
		case <-ticker.C:
		}
	}
}

func (process *managedProcess) checkHealthy(ctx context.Context) error {
	if process == nil || process.client == nil || process.exited() {
		return ErrPluginUnavailable
	}
	response, err := process.client.GetStatus(ctx, &centerpluginv1.GetStatusRequest{})
	if err != nil {
		return err
	}
	if err := centerpluginv1.ValidateStatusResponse(response); err != nil {
		return err
	}
	if response.Health != centerpluginv1.Health_HEALTH_HEALTHY {
		return errors.New("center plugin is not healthy")
	}
	return nil
}

func (process *managedProcess) stop(ctx context.Context) error {
	if process == nil {
		return nil
	}
	var result error
	process.closeOnce.Do(func() {
		if process.connection != nil {
			result = errors.Join(result, process.connection.Close())
		}
		if !process.exited() && process.command != nil && process.command.Process != nil {
			_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGTERM)
			timer := time.NewTimer(pluginStopTimeout)
			select {
			case <-process.done:
				timer.Stop()
			case <-ctx.Done():
				timer.Stop()
				_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
				<-process.done
				result = errors.Join(result, ctx.Err())
			case <-timer.C:
				_ = syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL)
				<-process.done
				result = errors.Join(result, errors.New("center plugin required SIGKILL"))
			}
		}
		if process.hostServer != nil {
			process.hostServer.Stop()
		}
		if process.hostListener != nil {
			_ = process.hostListener.Close()
		}
		for _, socket := range []string{process.pluginSocket, process.hostSocket} {
			if err := removeOwnedSocket(socket); err != nil {
				result = errors.Join(result, err)
			}
		}
	})
	return result
}

func (process *managedProcess) exited() bool {
	if process == nil {
		return true
	}
	select {
	case <-process.done:
		return true
	default:
		return false
	}
}

func verifyCenterArtifact(path string, value manifest.Manifest) error {
	var declaration *manifest.Artifact
	for index := range value.Artifacts {
		if value.Artifacts[index].Role == manifest.ArtifactCenter {
			declaration = &value.Artifacts[index]
			break
		}
	}
	if declaration == nil {
		return errors.New("center plugin manifest has no center artifact")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() != declaration.Size {
		return errors.New("installed center plugin artifact failed metadata verification")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("open installed center plugin artifact")
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != declaration.SHA256 {
		return errors.New("installed center plugin artifact failed SHA-256 verification")
	}
	return nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale plugin socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refusing to remove a non-socket from the plugin runtime directory")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale plugin socket: %w", err)
	}
	return nil
}

func removeOwnedSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("plugin replaced its runtime socket with a non-socket")
	}
	return os.Remove(path)
}
