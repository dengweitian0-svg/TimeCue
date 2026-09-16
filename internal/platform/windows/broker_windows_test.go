//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const brokerChildEnvironment = "TIMECUE_BROKER_CHILD"

// TestWindowsInstanceBrokerChildProcess 验证第二进程转发成功后只返回 AlreadyRunning。
func TestWindowsInstanceBrokerChildProcess(t *testing.T) {
	if os.Getenv(brokerChildEnvironment) != "1" {
		t.Skip("child-process helper")
	}
	name := os.Getenv("TIMECUE_BROKER_NAME")
	broker, err := NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions{ApplicationName: name})
	if err != nil {
		t.Fatal(err)
	}
	_, err = broker.Acquire(context.Background())
	if !errors.Is(err, bootstrap.ErrAlreadyRunning) {
		t.Fatalf("Acquire() error = %v, want ErrAlreadyRunning", err)
	}
}

// TestWindowsInstanceBrokerSecondProcess 验证主实例先监听再返回，第二进程不会打开数据库。
func TestWindowsInstanceBrokerSecondProcess(t *testing.T) {
	name := fmt.Sprintf("test-%d", time.Now().UnixNano())
	broker, err := NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions{ApplicationName: name})
	if err != nil {
		t.Fatalf("create broker: %v", err)
	}
	release, err := broker.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	t.Cleanup(func() { _ = release() })
	if err := broker.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	command := exec.Command(os.Args[0], "-test.run=TestWindowsInstanceBrokerChildProcess")
	command.Env = append(os.Environ(), brokerChildEnvironment+"=1", "TIMECUE_BROKER_NAME="+name)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("second process error = %v, output = %s", err, output)
	}
	select {
	case activation := <-broker.Events():
		if activation.Kind != platform.ActivationKindSecondInstance || activation.Target != nil {
			t.Fatalf("activation = %+v, want second instance without target", activation)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("main broker did not receive second-instance activation")
	}
	if strings.Contains(string(output), "sqlite") {
		t.Fatalf("child output unexpectedly mentions database: %s", output)
	}
}

// TestWindowsInstanceBrokerToastWithoutPrimary 验证无主实例时 Toast 激活不会进入运行生命周期。
func TestWindowsInstanceBrokerToastWithoutPrimary(t *testing.T) {
	name := fmt.Sprintf("toast-test-%d", time.Now().UnixNano())
	broker, err := NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions{
		ApplicationName: name,
		Args:            []string{"--toast", "7", "2026-09-16"},
	})
	if err != nil {
		t.Fatalf("create broker: %v", err)
	}
	if _, err := broker.Acquire(context.Background()); !errors.Is(err, ErrActivationWithoutInstance) {
		t.Fatalf("Acquire() error = %v, want ErrActivationWithoutInstance", err)
	}
}

// TestWindowsInstanceBrokerPublishCloseRaceGuard 验证进程内激活发布与 Close 不会向已关闭通道发送。
func TestWindowsInstanceBrokerPublishCloseRaceGuard(t *testing.T) {
	broker, err := NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions{ApplicationName: fmt.Sprintf("publish-test-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	activation := platform.Activation{Kind: platform.ActivationKindToast, Target: &domain.OccurrenceIdentity{ScheduleID: 7, OriginalDate: "2026-09-16"}}
	if err := broker.Publish(activation); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := <-broker.Events(); got.Kind != platform.ActivationKindToast || got.Target == nil {
		t.Fatalf("published activation = %+v", got)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(activation); err == nil {
		t.Fatal("Publish() error = nil after Close")
	}
}
