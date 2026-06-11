!macro NSIS_HOOK_PREINSTALL
  ; Upgrades can fail when the old sidecar engine keeps $INSTDIR\zhvpn.exe
  ; locked. Ask the existing CLI to stop first, then clear any leftover child
  ; process before NSIS copies the new sidecar into place.
  IfFileExists "$INSTDIR\zhvpn.exe" 0 done_stop_sidecar
    DetailPrint "Stopping existing zhvpn sidecar..."
    ExecWait '"$INSTDIR\zhvpn.exe" stop' $0
    Sleep 1000
    nsExec::ExecToLog 'taskkill /IM zhvpn.exe /T /F'
    Sleep 500
  done_stop_sidecar:
!macroend
