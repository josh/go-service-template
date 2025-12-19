{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  };

  outputs =
    { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      eachSystem = lib.attrsets.genAttrs systems;
    in
    {
      packages = eachSystem (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          example = pkgs.buildGoModule {
            pname = "example";
            version = "0.0.0";

            src = lib.sources.sourceByRegex ./. [
              ".*\.go$"
              "^go.mod$"
              "^go.sum$"
              "^systemd$"
              "^systemd\/.*$"
              "^test$"
              "^test\/.*$"
            ];

            vendorHash = null;

            env.CGO_ENABLED = 0;
            ldflags = [
              "-s"
              "-w"
            ];

            meta = {
              description = "Example package";
              homepage = "https://github.com/josh/example";
              license = lib.licenses.mit;
              platforms = lib.platforms.all;
              mainProgram = "example";
            };
          };

          docker-image = pkgs.dockerTools.buildImage {
            name = "example";
            config = {
              Cmd = [ "${lib.getExe self.packages.${system}.example}" ];
            };
          };

          default = self.packages.${system}.example;
        }
      );

      nixosModules.default =
        { config, lib, ... }:
        {
          options = {
            example = {
              enable = lib.options.mkEnableOption { };

              package = lib.options.mkOption {
                type = lib.types.package;
                default = self.packages.${config.nixpkgs.system}.default;
              };

              port = lib.options.mkOption {
                type = lib.types.int;
                default = 8080;
              };
            };
          };

          config = lib.modules.mkIf config.example.enable {
            systemd.services.example = {
              description = "Example service";
              requires = [ "example.socket" ];

              serviceConfig = {
                ExecStart = "${lib.getExe config.example.package}";
                DynamicUser = true;
              };
            };

            systemd.sockets.example = {
              description = "Example socket";
              service = "example.service";
              wantedBy = [ "sockets.target" ];
              socketConfig.ListenStream = config.example.port;
            };
          };
        };
    };
}
