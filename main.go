package main

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

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
func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, ctx.Stack())
		distribution := cfg.Require("distribution")
		sshUsername := cfg.Require("sshUsername")

		installCmd, err := installCmd(distribution)
		updateCmd, err := updateCmd(distribution)

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

		commands := []struct {
			name string
			cmd  string
		}{
			{"update-system", updateCmd},
			{"install-packages", fmt.Sprintf("sudo %s git zsh", installCmd)},
			{"setup-home", "rm -rf $HOME/github && mkdir $HOME/github"},
			{"setup-config", "git clone https://github.com/ismail/config.git $HOME/github/config"},
			{"setup-hacks", "git clone https://github.com/ismail/hacks.git $HOME/github/hacks"},
			{"run-setup", "cd $HOME/github/config && ./setup.sh && cd $HOME/github/hacks && ./setup.sh"},
			{"use-zsh", "sudo chsh -s /bin/zsh ismail"},
			{"install-starship", "curl -sS https://starship.rs/install.sh | sudo sh -s -- -y"},
			{"starship-disable-container", "mkdir -p ~/.config && echo \"[container]\ndisabled = true\" > ~/.config/starship.toml"},
			{"use-starship", "echo 'eval \"$(starship init zsh)\"' > ~/.zshrc-local"},
		}

		var lastResource pulumi.Resource

		for _, c := range commands {
			ctx.Log.Info(fmt.Sprintf("Running command: '%s'", c.cmd), nil)

			var opts []pulumi.ResourceOption
			if lastResource != nil {
				opts = append(opts, pulumi.DependsOn([]pulumi.Resource{lastResource}))
			}

			r, err := remote.NewCommand(ctx, c.name, &remote.CommandArgs{
				Connection: connection,
				Create:     pulumi.String(c.cmd),
			}, opts...)

			if err != nil {
				fmt.Printf("failed to run command '%s': %v", c.cmd, err)
				return nil
			}
			lastResource = r
		}

		ctx.Log.Info("Packages installed successfully!", nil)

		return nil
	})
}
