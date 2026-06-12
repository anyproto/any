{
  description = "any dev shell — Go toolchain + runtime deps for the local embedder";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gnumake
            curl
            git
          ];

          # The local embedder (index.embedder: local) dlopens prebuilt
          # llama.cpp shared libs via yzma/purego at runtime:
          #  - libffi: required by jupiterrider/ffi on Linux (bundled on macOS)
          #  - libstdc++ / libgomp: NEEDED by the ubuntu-built llama.cpp libs
          shellHook = pkgs.lib.optionalString pkgs.stdenv.isLinux ''
            export LD_LIBRARY_PATH=${pkgs.lib.makeLibraryPath [
              pkgs.libffi
              pkgs.stdenv.cc.cc.lib # libstdc++ + libgomp
            ]}''${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}
          '';
        };
      });
    };
}
