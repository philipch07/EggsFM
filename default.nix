# kinda pointless and bad
{ pkgs, ... }:
let
  frontend = pkgs.buildNpmPackage {
    pname = "egssfm_frontend";
    version = "0.1.0";
    src = ./web;

    npmDepsHash = "sha256-zgxzwLSZNLKRopmRVgQR3YXwFgkrE2foxz1FHLOkfag=";

    installPhase = ''
      runHook preInstall

      mkdir -p $out
      cp -r build $out/build

      runHook postInstall
    '';
  };
in
pkgs.buildGoModule {
  pname = "eggsfm";
  version = "0.1.0";
  src = ./.;

  vendorHash = "sha256-Gxjeh4aGUHpeAtUMHViXdrvjUCRIhBG1vwU+VagVkv4=";

  checkPhase = null;

  postInstall = ''
    mkdir -p $out/web/
    cp -r ${frontend}/build $out/web/build
  '';
}
