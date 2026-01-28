{
  description = "AgentFS - Instant checkpoint and restore for macOS projects";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        agentfs = pkgs.buildGoModule {
          pname = "agentfs";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-YS4tbIuJ4hMq8M4nZIXxuC38A5OYY6jiYvYVEDkGxJc=";
          subPackages = [ "cmd/agentfs" ];
        };
      in
      {
        packages.default = agentfs;
        packages.agentfs = agentfs;

        devShells.default = pkgs.mkShell {
          packages = [
            # agentfs - removed to use global go run wrapper for live development
            pkgs.go
            pkgs.gopls
            pkgs.gotools
            pkgs.go-tools
            pkgs.delve
            pkgs.sqlite
          ];

          shellHook = ''
            export GOPATH="$PWD/.go"
            export PATH="$GOPATH/bin:$PATH"
          '';
        };

        apps.default = {
          type = "app";
          program = "${agentfs}/bin/agentfs";
        };
      });
}
