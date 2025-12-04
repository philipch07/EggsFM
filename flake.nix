{
  description = "EggsFM";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-25.11";
    flake-utils.url = "github:numtide/flake-utils";
  };
  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells = {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go
              nodejs_24
            ];
          };
        };
        apps = {
          default =
            let eggsfm = pkgs.writeShellApplication {
              name = "eggsfm";

              runtimeInputs = with pkgs; [
                nodejs_24
                go
              ];

              text = ''
                set -euo pipefail

                cd web
                npm install
                npm run build
                cd ..
                go run .
              '';
            };
            in
            {
              type = "app";
              program = "${eggsfm}/bin/eggsfm";
            };
        };
      }
    );
}
