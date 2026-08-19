{
  description = "TUI for systemd service cpu/memory/network usage, failures, restarts and logs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      # unitop talks to systemd, so Linux only — but both common arches.
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = "0.3.2";
    in {
      packages = forAllSystems (pkgs: rec {
        unitop = pkgs.buildGoModule {
          pname = "unitop";
          inherit version;
          src = ./src;
          vendorHash = null; # 'go mod vendor' was already run

          # Static: the binary is meant to be scp'd onto any host, including
          # ones with a different libc than the machine that built it.
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=${version}" ];

          meta = with pkgs.lib; {
            description = "TUI for systemd service cpu/memory/network usage, failures, restarts and logs";
            homepage = "https://github.com/sagar-chandarana/unitop-go";
            license = licenses.mit;
            mainProgram = "unitop";
            platforms = platforms.linux;
          };
        };
        default = unitop;
      });

      overlays.default = final: prev: {
        unitop = self.packages.${final.system}.unitop;
      };

      apps = forAllSystems (pkgs: rec {
        unitop = {
          type = "app";
          program = "${self.packages.${pkgs.system}.unitop}/bin/unitop";
        };
        default = unitop;

        # Regenerate the four images in docs/ from the current build. Run from
        # the repo root — it writes there:
        #
        #   nix run .#screenshots
        #
        # The journal is invented (docs/helpers/fake-journalctl.sh) so this
        # works anywhere, including where the host's own journal is unreadable;
        # the unit table and host stats are the real machine's. The hostname is
        # replaced with server1.local on the way out.
        screenshots = {
          type = "app";
          program = "${pkgs.writeShellApplication {
            name = "unitop-screenshots";
            runtimeInputs = with pkgs; [
              tmux perl gawk gnused coreutils termshot pngquant imagemagick systemd procps
            ];
            text = ''
              if [ ! -x docs/helpers/screenshot.sh ]; then
                echo "run this from the repo root: docs/helpers/screenshot.sh is not here" >&2
                exit 1
              fi
              export UNITOP=${self.packages.${pkgs.system}.unitop}/bin/unitop
              export REAL_JOURNALCTL=${pkgs.systemd}/bin/journalctl
              export JOURNAL_REMOTE=${pkgs.systemd}/lib/systemd/systemd-journal-remote
              export FAKE_JOURNAL=1
              # Render in a real terminal theme. termshot's own palette ignores SGR 2
              # entirely, so every dimmed label came out at full strength and the
              # images showed a flatter interface than the one that ships.
              export THEME="''${THEME:-duskfox}"

              D="Down Down Down Down Down"
              # shellcheck disable=SC2086
              {
                docs/helpers/screenshot.sh docs/main.png 132 30 -s name -f systemd- -- $D
                docs/helpers/screenshot.sh docs/full.png 132 30 -s name -f systemd- -- $D Enter
                docs/helpers/screenshot.sh docs/tree.png 132 30 -t -s cpu -- Down Down Down
                docs/helpers/screenshot.sh docs/menu.png 132 30 -s name -f systemd- -- $D x Down
              }
            '';
          }}/bin/unitop-screenshots";
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          buildInputs = [ pkgs.go pkgs.gopls pkgs.gotools pkgs.delve ];
          shellHook = ''
            export GOPATH=$HOME/go
            export PATH=$PATH:$GOPATH/bin
          '';
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixpkgs-fmt);
    };
}
