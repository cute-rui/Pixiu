OutFile "ClashVergeServiceInstaller.exe"

InstallDir "$PROGRAMFILES\ClashVergeService"

Page directory
Page instfiles

Section "Install"
    SetOutPath $INSTDIR

    ;FILES_PLACEHOLDER

    WriteUninstaller "$INSTDIR\Uninstall.exe"

    ExecShell "" "$INSTDIR\pixiu-service-install.exe"
SectionEnd

Section "Uninstall"
    Delete "$INSTDIR\*.exe"
    Delete "$INSTDIR\Uninstall.exe"
    RMDir "$INSTDIR"
SectionEnd
