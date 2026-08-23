package control

import (
	"reflect"
	"testing"
)

func TestHostctlInvocation_UsesSudoWhenTemperciUser(t *testing.T) {
	name, args := hostctlInvocation(1000, "temperci", "/usr/local/bin/temperci-hostctl", "restart", "all")
	if name != "sudo" {
		t.Fatalf("name=%q want sudo", name)
	}
	want := []string{"-n", "/usr/local/bin/temperci-hostctl", "restart", "all"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want %v", args, want)
	}
}

func TestHostctlInvocation_RunsDirectlyAsRoot(t *testing.T) {
	name, args := hostctlInvocation(0, "root", "/usr/local/bin/temperci-hostctl", "status", "control")
	if name != "/usr/local/bin/temperci-hostctl" {
		t.Fatalf("name=%q", name)
	}
	want := []string{"status", "control"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want %v", args, want)
	}
}

func TestHostctlInvocation_NoSudoForOtherUsers(t *testing.T) {
	name, args := hostctlInvocation(501, "twan", "/tmp/fake-hostctl", "status", "agent")
	if name != "/tmp/fake-hostctl" {
		t.Fatalf("name=%q", name)
	}
	want := []string{"status", "agent"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v want %v", args, want)
	}
}
