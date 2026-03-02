{ config, pkgs, lib, ... }:

{
  nixpkgs.config.allowUnfree = true;
  nixpkgs.config.permittedInsecurePackages = [
    "broadcom-sta-6.30.223.271-59-6.12.63"
  ];

  boot.kernelModules = [ "wl" ];
  boot.extraModulePackages = [ config.boot.kernelPackages.broadcom_sta ];
  boot.blacklistedKernelModules = [ "b43" "bcma" "brcmfmac" "brcmsmac" ];

  environment.systemPackages = with pkgs; [
    vim
    git
    wget
    parted
  ];

  hardware.wirelessRegulatoryDatabase = true;
}
