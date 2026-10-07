!include "LogicLib.nsh"
!include "${__FILEDIR__}\installer-validation.generated.nsh"
!define ZHVPN_HASH_WRITER "${__FILEDIR__}\..\scripts\write-installer-hashes.ps1"
Var ZhPayloadStage
Var ZhParentPath
Var ZhParentHandle
Var ZhStageHandle
Var ZhOwnerSID

!macro ZHVPN_CLOSE_PINS
  ${If} $ZhStageHandle != ""
  ${AndIf} $ZhStageHandle != -1
    System::Call 'kernel32::CloseHandle(p $ZhStageHandle)'
    StrCpy $ZhStageHandle ""
  ${EndIf}
  ${If} $ZhParentHandle != ""
  ${AndIf} $ZhParentHandle != -1
    System::Call 'kernel32::CloseHandle(p $ZhParentHandle)'
    StrCpy $ZhParentHandle ""
  ${EndIf}
!macroend

!macro ZHVPN_STAGE_FAILURE REASON
  !insertmacro ZHVPN_CLOSE_PINS
  SetErrorLevel 12
  Abort "${REASON}"
!macroend

!macro ZHVPN_REFUSE_EXISTING_REGISTRATION VALUE
  ${If} "${VALUE}" != ""
    !insertmacro ZHVPN_CLOSE_PINS
    SetErrorLevel 12
    IfSilent +2
    MessageBox MB_OK|MB_ICONSTOP "当前安装器仅支持全新安装。检测到已有安装，已在调用旧卸载器前停止。升级与自动卸载尚未通过验收，请保留现有安装和恢复记录。"
    Abort "已有安装未变动：升级迁移尚未通过验收。"
  ${EndIf}
!macroend

; Until a signed, recoverable maintenance protocol exists, only a fresh target
; is supported. These checks run in .onInit, before Tauri's old-uninstaller page,
; and again immediately before extraction/publish. No existing installation is
; overwritten and no installed uninstaller is executed.
!macro ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION
  Push $0
  ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCTNAME}" "UninstallString"
  ${If} $0 == ""
    ReadRegStr $0 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCTNAME}" "UninstallString"
  ${EndIf}
  !insertmacro ZHVPN_REFUSE_EXISTING_REGISTRATION "$0"
  Pop $0
!macroend

!macro ZHVPN_REQUIRE_FRESH_TARGET
  Push $0
  Push $1
  System::Call 'kernel32::GetFileAttributesW(w "$INSTDIR") i.r0 ?e'
  Pop $1
  ${If} $0 != -1
    StrCpy $1 0
  ${EndIf}
  ${If} $1 != 2
  ${AndIf} $1 != 3
    Pop $1
    Pop $0
    !insertmacro ZHVPN_CLOSE_PINS
    SetErrorLevel 12
    IfSilent +2
    MessageBox MB_OK|MB_ICONSTOP "目标目录已存在或无法确认全新。当前安装器不会覆盖、停止或卸载已有安装，请选择尚未创建的全新目录。"
    Abort "安装未执行：目标不是可验证的全新目录。"
  ${EndIf}
  Pop $1
  Pop $0
!macroend

!macro ZHVPN_REQUIRE_EXTRACTION_SUCCESS
  ${If} ${Errors}
    IfSilent +2
    MessageBox MB_OK|MB_ICONSTOP "完整安装包解压失败，未发布到目标目录。请保留现有安装后重试。"
    !insertmacro ZHVPN_STAGE_FAILURE "完整安装包未能解压，目标未变动。"
  ${EndIf}
!macroend

!macro ZHVPN_CREATE_FRESH_STAGE
  ; The parent is pinned without FILE_SHARE_DELETE for the complete window.
  ; A fixed embedded read-only program rejects reparse/remote paths and any
  ; untrusted namespace/DACL writer, returning only this token's owner SID.
  ; Neither the GUID nor a writable script file is the permission boundary.
  Push $0
  Push $1
  Push $2
  System::Call 'kernel32::GetFullPathNameW(w "$INSTDIR", i ${NSIS_MAX_STRLEN}, w.r0, p 0) i.r1'
  ${If} $1 == 0
  ${OrIf} $1 >= ${NSIS_MAX_STRLEN}
    !insertmacro ZHVPN_STAGE_FAILURE "目标路径无法完整解析；目标未变动。"
  ${EndIf}
  StrCpy $INSTDIR $0
  ${GetParent} "$INSTDIR" $ZhParentPath
  System::Call 'kernel32::CreateFileW(w "$ZhParentPath", i 0x20080, i 3, p 0, i 3, i 0x02200000, p 0) p.r0'
  StrCpy $ZhParentHandle $0
  ${If} $ZhParentHandle == -1
    !insertmacro ZHVPN_STAGE_FAILURE "父目录无法锁定，目标未变动。"
  ${EndIf}
  System::Call 'kernel32::SetEnvironmentVariableW(w "ZHVPN_INSTALL_PARENT", w "$ZhParentPath")'
  !insertmacro ZHVPN_SET_PARENT_PROGRAM
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -EncodedCommand ${ZHVPN_PARENT_VALIDATOR}'
  Pop $0
  Pop $ZhOwnerSID
  ${If} $0 != 0
  ${OrIf} $ZhOwnerSID == ""
    !insertmacro ZHVPN_STAGE_FAILURE "父目录权限、所有者或路径无法验证，目标未变动。"
  ${EndIf}
  System::Call 'ole32::CoCreateGuid(g .r0) i.r1'
  ${If} $1 != 0
    !insertmacro ZHVPN_STAGE_FAILURE "无法创建完整安装目录身份；目标未变动。"
  ${EndIf}
  StrCpy $ZhPayloadStage "$INSTDIR.zhstage-$0"
  StrCpy $2 "O:$ZhOwnerSID"
  StrCpy $2 "$2D:P(A;OICI;FA;;;$ZhOwnerSID)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
  System::Call 'advapi32::ConvertStringSecurityDescriptorToSecurityDescriptorW(w "$2", i 1, *p.r0, p 0) i.r2'
  ${If} $2 == 0
    !insertmacro ZHVPN_STAGE_FAILURE "无法建立私有目录权限；目标未变动。"
  ${EndIf}
  ; Tauri's pinned NSIS distribution is a 32-bit installer, on both target
  ; architectures: SECURITY_ATTRIBUTES is exactly three 32-bit fields.
  System::Call '*(i 12, p r0, i 0) p.r1'
  System::Call 'kernel32::CreateDirectoryW(w "$ZhPayloadStage", p r1) i.r2'
  System::Free $1
  System::Call 'kernel32::LocalFree(p r0)'
  ${If} $2 == 0
    !insertmacro ZHVPN_STAGE_FAILURE "无法创建全新私有安装目录；目标未变动。"
  ${EndIf}
  System::Call 'kernel32::CreateFileW(w "$ZhPayloadStage", i 0x20080, i 3, p 0, i 3, i 0x02200000, p 0) p.r0'
  StrCpy $ZhStageHandle $0
  ${If} $ZhStageHandle == -1
    !insertmacro ZHVPN_STAGE_FAILURE "私有安装目录无法锁定；目标未变动。"
  ${EndIf}
  Pop $2
  Pop $1
  Pop $0
  SetOutPath "$ZhPayloadStage"
!macroend

!macro ZHVPN_PUBLISH_FRESH_PAYLOAD
  System::Call 'kernel32::SetEnvironmentVariableW(w "ZHVPN_STAGE_DIR", w "$ZhPayloadStage")'
  System::Call 'kernel32::SetEnvironmentVariableW(w "ZHVPN_GUI_SHA256", w "${ZHVPN_GUI_SHA256}")'
  System::Call 'kernel32::SetEnvironmentVariableW(w "ZHVPN_CLI_SHA256", w "${ZHVPN_CLI_SHA256}")'
  !insertmacro ZHVPN_SET_PAYLOAD_PROGRAM
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -EncodedCommand ${ZHVPN_PAYLOAD_VALIDATOR}'
  Pop $0
  Pop $1
  ${If} $0 != 0
    !insertmacro ZHVPN_STAGE_FAILURE "完整 GUI/CLI 权限或 SHA256 不匹配，拒绝发布。"
  ${EndIf}
  !insertmacro ZHVPN_REQUIRE_FRESH_TARGET
  ; Release only the private stage's handles/cwd for the single no-overwrite
  ; directory rename. The trusted parent remains pinned through publication.
  SetOutPath "$PLUGINSDIR"
  System::Call 'kernel32::CloseHandle(p $ZhStageHandle)'
  StrCpy $ZhStageHandle ""
  ClearErrors
  Rename "$ZhPayloadStage" "$INSTDIR"
  ${If} ${Errors}
    IfSilent +2
    MessageBox MB_OK|MB_ICONSTOP "完整安装目录无法安全发布。目标可能被其他程序创建或锁定；未覆盖目标。请确认全新目录后重试。"
    !insertmacro ZHVPN_STAGE_FAILURE "目录发布失败；未覆盖、分批复制或安排重启替换。"
  ${EndIf}
  !insertmacro ZHVPN_CLOSE_PINS
  SetOutPath "$INSTDIR"
!macroend

; Fresh-only maintenance must never invoke Tauri's by-name process killer.
!ifmacrodef CheckIfAppIsRunning
  !macroundef CheckIfAppIsRunning
!endif
!macro CheckIfAppIsRunning executableName productName
  !insertmacro ZHVPN_REQUIRE_FRESH_TARGET
!macroend

!macro NSIS_HOOK_PREINSTALL
  !insertmacro ZHVPN_REQUIRE_NO_EXISTING_REGISTRATION
  !insertmacro ZHVPN_REQUIRE_FRESH_TARGET
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ; The previous per-file delete path had the same late-start race as upgrade.
  ; Never erase binaries or app data until whole-maintenance acceptance exists.
  SetErrorLevel 12
  IfSilent +2
  MessageBox MB_OK|MB_ICONSTOP "自动卸载尚未通过完整恢复与并发验收。当前安装和客户端恢复记录已保留，请使用受控维护流程。"
  Abort "自动卸载未执行：安装和恢复记录已保留。"
!macroend
