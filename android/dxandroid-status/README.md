# Daxiang Android Status

Minimal native Android app for checking the rooted `dxandroid-egress` process on the phone.

## Features

- Foreground service with a persistent Android notification.
- Simple native status screen.
- Reads live status through `su`:
  - root availability
  - `dxandroid-egress` PID
  - cellular/default route
  - `wg0` address
  - `10.66.0.101:1080` listener
  - recent service and egress logs

## Build

Install Android SDK command-line tools, then set `local.properties`:

```properties
sdk.dir=C\:\\Path\\To\\Android\\Sdk
```

Build:

```powershell
.\gradlew.bat assembleDebug
```

Install:

```powershell
$adb="$env:LOCALAPPDATA\Android\platform-tools\adb.exe"
& $adb install -r app\build\outputs\apk\debug\app-debug.apk
& $adb shell "pm grant dev.daxiang.dxandroidstatus android.permission.POST_NOTIFICATIONS"
& $adb shell "am start -n dev.daxiang.dxandroidstatus/.MainActivity"
```

The app is intentionally a status surface only. The actual network service is still owned by Magisk and `/data/adb/service.d/99-dxandroid-egress.sh`.
