package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const commonPackages = "bpftrace clang cmake curl gcc gdb git less llvm man-db mold pkgconf sysstat zsh"
const cargoPackages = "bat csvlens hexyl hyperfine xsv"

func installCmd(distribution string) (string, error) {
	switch distribution {
	case "fedora":
		return "sudo dnf install -y", nil
	case "ubuntu", "debian":
		return "sudo apt-get install -y", nil
	default:
		return "", fmt.Errorf("unsupported distribution: %s", distribution)
	}
}

func updateCmd(distribution string) (string, error) {
	switch distribution {
	case "fedora":
		return "sudo dnf update -y", nil
	case "ubuntu", "debian":
		return "sudo apt-get update && sudo apt-get dist-upgrade -y", nil
	default:
		return "", fmt.Errorf("unsupported distribution: %s", distribution)
	}
}

func extraPackagesForDistro(distribution string) []string {
	switch distribution {
	case "fedora":
		return []string{"fedora-packager", "fedora-review", "gcc-c++", "ninja", "perf"}
	case "ubuntu", "debian":
		return []string{"g++", "linux-tools-virtual", "ninja-build"}
	default:
		return []string{}
	}
}

func runIndependentCommands(ctx *pulumi.Context, commands []struct {
	name string
	cmd  string
}, connection remote.ConnectionArgs) error {
	for _, c := range commands {
		ctx.Log.Info(fmt.Sprintf("%s: '%s'", c.name, c.cmd), nil)

		_, err := remote.NewCommand(ctx, c.name, &remote.CommandArgs{
			Connection: connection,
			Create:     pulumi.String(c.cmd),
			Triggers:   pulumi.Array{pulumi.String(c.cmd)},
		})
		if err != nil {
			return fmt.Errorf("failed to run command '%s': %w", c.cmd, err)
		}
	}
	return nil
}

func runOrderedCommands(ctx *pulumi.Context, update_cmd *remote.Command, commands []struct {
	name string
	cmd  string
}, connection remote.ConnectionArgs) error {
	var lastResource pulumi.Resource = update_cmd

	for _, c := range commands {

		ctx.Log.Info(fmt.Sprintf("%s: '%s'", c.name, c.cmd), nil)
		r, err := remote.NewCommand(ctx, c.name, &remote.CommandArgs{
			Connection: connection,
			Create:     pulumi.String(c.cmd),
			Triggers:   pulumi.Array{pulumi.String(c.cmd)},
		}, pulumi.DependsOn([]pulumi.Resource{lastResource}))

		if err != nil {
			return fmt.Errorf("failed to run command '%s': %w", c.cmd, err)
		}

		lastResource = r
	}
	return nil
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, ctx.Stack())
		distribution := cfg.Require("distribution")
		sshUsername := cfg.Require("sshUsername")

		key, err := os.ReadFile(os.ExpandEnv("$HOME/.orbstack/ssh/id_ed25519"))
		if err != nil {
			return fmt.Errorf("failed to read private key: %w", err)
		}

		connection := remote.ConnectionArgs{
			Host:       pulumi.String("localhost"),
			Port:       pulumi.Float64(32222),
			User:       pulumi.String(sshUsername),
			PrivateKey: pulumi.String(string(key)),
		}

		installCmd, err := installCmd(distribution)
		if err != nil {
			return fmt.Errorf("failed to get install command: %w", err)
		}

		updateCmd, err := updateCmd(distribution)
		if err != nil {
			return fmt.Errorf("failed to get update command: %w", err)
		}

		extraPackages := extraPackagesForDistro(distribution)
		setup_commands := []struct {
			name string
			cmd  string
		}{
			{"install-packages", fmt.Sprintf("%s %s %s", installCmd, commonPackages, strings.Join(extraPackages, " "))},
			{"install-cargo", "rm -rf ~/.cargo ~/.rustup && curl -LsSf https://sh.rustup.rs | sh -s -- -y --no-modify-path"},
			// zsh is not setup yet, we need full path to cargo
			{"install-cargo-packages", fmt.Sprintf("~/.cargo/bin/cargo install %s", cargoPackages)},
			{"setup-config", "rm -rf ~/github/config && git clone https://github.com/ismail/config.git ~/github/config && ~/github/config/setup.sh"},
			{"setup-hacks", "rm -rf ~/github/hacks && git clone https://github.com/ismail/hacks.git ~/github/hacks && ~/github/hacks/setup.sh"},
			{"set-zlogin", "echo 'path+=(~/.local/bin ~/.cargo/bin $path)\n\neval \"$(starship init zsh)\"' > ~/.zlogin"},
			{"use-zsh", "sudo chsh -s /bin/zsh ismail"},
		}

		// These run independently
		extra_commands := []struct {
			name string
			cmd  string
		}{
			{"install-starship", "curl -sS https://starship.rs/install.sh | sudo sh -s -- -y"},
			{"install-uv", "curl -LsSf https://astral.sh/uv/install.sh | UV_NO_MODIFY_PATH=1 sh"},
			{"starship-disable-container", "mkdir -p ~/.config && echo \"[container]\ndisabled = true\" > ~/.config/starship.toml"},
		}

		// We always update the system
		base_cmd, err := remote.NewCommand(ctx, "update-system", &remote.CommandArgs{
			Connection: connection,
			Create:     pulumi.String(updateCmd),
			Triggers:   pulumi.Array{pulumi.String(time.Now().Format(time.RFC3339))},
		})

		if err != nil {
			return fmt.Errorf("failed to update the system: %w", err)
		}

		// Setup the base system
		if err := runOrderedCommands(ctx, base_cmd, setup_commands, connection); err != nil {
			ctx.Log.Error(fmt.Sprintf("Failed to run base commands: %v", err), nil)
			return err
		}

		// The rest
		if err := runIndependentCommands(ctx, extra_commands, connection); err != nil {
			ctx.Log.Error(fmt.Sprintf("Failed to run setup commands: %v", err), nil)
			return err
		}

		ctx.Log.Info(fmt.Sprintf("%s setup complete.", distribution), nil)

		return nil
	})
}
