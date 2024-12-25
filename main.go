package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const commonPackages = "curl less git man-db zsh"

func installCmd(distribution string) (string, error) {
	switch distribution {
	case "fedora":
		return "dnf install -y", nil
	case "ubuntu", "debian":
		return "apt-get install -y", nil
	default:
		return "", fmt.Errorf("unsupported distribution: %s", distribution)
	}
}

func updateCmd(distribution string) (string, error) {
	switch distribution {
	case "fedora":
		return "sudo dnf update -y", nil
	case "ubuntu", "debian":
		return "sudo bash -c \"apt-get update && apt-get dist-upgrade -y\"", nil
	default:
		return "", fmt.Errorf("unsupported distribution: %s", distribution)
	}
}

func extraPackagesForDistro(distribution string) []string {
	switch distribution {
	case "fedora":
		return []string{"fedora-packager", "fedora-review"}
	default:
		return nil
	}
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

		// These commands need to be run in order
		base_commands := []struct {
			name string
			cmd  string
		}{
			{"update-system", updateCmd},
			{"install-packages", fmt.Sprintf("sudo %s %s %s", installCmd, commonPackages, strings.Join(extraPackages, " "))},
			{"use-zsh", "sudo chsh -s /bin/zsh ismail"},
		}

		// These run independently
		commands := []struct {
			name string
			cmd  string
		}{
			{"install-cargo", "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --no-modify-path"},
			{"setup-config", "rm -rf ~/github/config && git clone https://github.com/ismail/config.git ~/github/config && ~/github/config/setup.sh"},
			{"setup-hacks", "rm -rf ~/github/hacks && git clone https://github.com/ismail/hacks.git ~/github/hacks && ~/github/hacks/setup.sh"},
			{"install-starship", "curl -sS https://starship.rs/install.sh | sudo sh -s -- -y"},
			{"starship-disable-container", "mkdir -p ~/.config && echo \"[container]\ndisabled = true\" > ~/.config/starship.toml"},
			{"set-zlogin", "echo 'path+=(~/.cargo/bin $path)\n\neval \"$(starship init zsh)\"' > ~/.zlogin"},
		}

		// Run base commands in order
		var lastResource pulumi.Resource

		for _, c := range base_commands {
			ctx.Log.Info(fmt.Sprintf("Running command: '%s'", c.cmd), nil)

			var opts []pulumi.ResourceOption
			if lastResource != nil {
				opts = append(opts, pulumi.DependsOn([]pulumi.Resource{lastResource}))
			}

			r, err := remote.NewCommand(ctx, c.name, &remote.CommandArgs{
				Connection: connection,
				Create:     pulumi.String(c.cmd),
				Triggers:   pulumi.Array{pulumi.String(c.cmd)},
			}, opts...)

			if err != nil {
				fmt.Printf("failed to run command '%s': %v", c.cmd, err)
				return nil
			}
			lastResource = r
		}

		// Run independent commands
		for _, c := range commands {
			ctx.Log.Info(fmt.Sprintf("Running command: '%s'", c.cmd), nil)

			_, err := remote.NewCommand(ctx, c.name, &remote.CommandArgs{
				Connection: connection,
				Create:     pulumi.String(c.cmd),
				Triggers:   pulumi.Array{pulumi.String(c.cmd)},
			})
			if err != nil {
				return fmt.Errorf("failed to run command %s: %w", c.name, err)
			}
		}

		ctx.Log.Info(fmt.Sprintf("%s setup complete.", distribution), nil)

		return nil
	})
}
