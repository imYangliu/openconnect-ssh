package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/discovery"
)

func TestSetupRejectsShadowedRecursiveAliasWithoutOverwrite(t *testing.T) {
	if os.Getenv("OCH_TEST_RECURSIVE_SETUP") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupRejectsShadowedRecursiveAliasWithoutOverwrite$")
		cmd.Env = append(os.Environ(), "OCH_TEST_RECURSIVE_SETUP=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			if len(output) > 4000 {
				output = output[:4000]
			}
			t.Fatalf("setup did not safely reject recursive YAML: %v\n%s", err, output)
		}
		return
	}
	debug.SetMaxStack(1 << 20)
	home := workspace(t)
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	data := []byte("socket: " + filepath.Join(home, "run", "och.sock") + "\nprofiles:\n  real:\n    protocol: anyconnect\n    server: https://real.invalid\n<<:\n  profiles: &unused\n    self: *unused\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("configuration rejected before setup: %v", err)
	}
	var out bytes.Buffer
	r := Runner{Home: home, Output: &out, Discover: func(context.Context, string, string) (discovery.Result, error) {
		return discovery.Result{Protocol: "anyconnect", Server: "https://new.invalid"}, nil
	}}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://new.invalid", Name: "added", Username: "new", Listen: "127.0.0.1:1081", SkipLogin: true, NoStart: true, NonInteractive: true})
	if err == nil || !strings.Contains(err.Error(), "recursive YAML") {
		t.Fatalf("expected actionable recursive YAML error, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("failed setup overwrote original configuration")
	}
}

func TestSetupBoundsShadowedAcyclicAliases(t *testing.T) {
	mode := os.Getenv("OCH_TEST_SETUP_ALIAS_LIMIT")
	if mode == "" {
		for _, kind := range []string{"expansion", "depth", "scalar-size", "ordinary"} {
			t.Run(kind, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSetupBoundsShadowedAcyclicAliases$")
				cmd.Env = append(os.Environ(), "OCH_TEST_SETUP_ALIAS_LIMIT="+kind)
				output, err := cmd.CombinedOutput()
				if err != nil {
					if len(output) > 4000 {
						output = output[:4000]
					}
					t.Fatalf("unsafe or incorrect YAML expansion: %v\n%s", err, output)
				}
			})
		}
		return
	}
	debug.SetMaxStack(1 << 20)
	home := workspace(t)
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	var hidden strings.Builder
	if mode == "scalar-size" {
		hidden.WriteString("    large: &large " + strings.Repeat("x", 100000) + "\n")
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&hidden, "    copy%d: *large\n", i)
		}
	} else if mode == "depth" {
		hidden.WriteString("    nested: " + strings.Repeat("[", 300) + "value" + strings.Repeat("]", 300) + "\n")
	} else {
		levels := 18
		if mode == "ordinary" {
			levels = 5
		}
		hidden.WriteString("    n0: &n0 [value, value]\n")
		for i := 1; i <= levels; i++ {
			fmt.Fprintf(&hidden, "    n%d: &n%d [*n%d, *n%d]\n", i, i, i-1, i-1)
		}
	}
	data := []byte("socket: " + filepath.Join(home, "run", "och.sock") + "\nprofiles:\n  real:\n    protocol: anyconnect\n    server: https://real.invalid\n<<:\n  profiles:\n" + hidden.String())
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("configuration rejected before setup: %v", err)
	}
	var out bytes.Buffer
	r := Runner{Home: home, Output: &out, Discover: func(context.Context, string, string) (discovery.Result, error) {
		return discovery.Result{Protocol: "anyconnect", Server: "https://new.invalid"}, nil
	}}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://new.invalid", Name: "added", Username: "new", Listen: "127.0.0.1:1081", SkipLogin: true, NoStart: true, NonInteractive: true})
	if mode == "ordinary" {
		if err != nil {
			t.Fatalf("ordinary acyclic aliases rejected: %v", err)
		}
		c, err := config.Load(path)
		if err != nil || c.Profiles["real"].Server != "https://real.invalid" || c.Profiles["added"].Server != "https://new.invalid" {
			t.Fatalf("effective mappings changed: %+v %v", c, err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "too large or deeply nested") {
		t.Fatalf("expected safe expansion limit error, got %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(after, data) {
		t.Fatal("failed setup overwrote original configuration")
	}
}
