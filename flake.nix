{
  description = "TUI for systemd service cpu/memory/network usage, failures, restarts and logs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      # unitop talks to systemd, so Linux only — but both common arches.
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = "0.1.1";
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
