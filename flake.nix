{
  description = "TUI for systemd service cpu/memory/network usage, failures, restarts and logs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      pname = "unitop";
      version = "1.0.0";
    in {
      packages.${system}.default = pkgs.buildGoModule {
        inherit pname version;
        src = ./src;
        vendorHash = null; # 'go mod vendor' was already run

        # Static: the binary is meant to be scp'd onto any host, including
        # ones with a different libc than the machine that built it.
        env.CGO_ENABLED = 0;
        ldflags = [ "-s" "-w" "-X main.version=${version}" ];

        meta = {
          description = "TUI for systemd service cpu/memory/network usage, failures, restarts and logs";
          mainProgram = "unitop";
        };
      };

      apps.${system}.default = {
        type = "app";
        program = "${self.packages.${system}.default}/bin/unitop";
      };

      devShells.${system}.default = pkgs.mkShell {
        buildInputs = [
          pkgs.go
          pkgs.gopls
          pkgs.gotools
          pkgs.delve
        ];
        shellHook = ''
          export GOPATH=$HOME/go
          export PATH=$PATH:$GOPATH/bin
        '';
      };
    };
}
