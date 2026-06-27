# UVPrinterLogMonitor

A Simple monitor for Hosonsoft UV printer logs.

It watches:

```text
<printer app>\Log\main\Log[yyyy_mm_dd].txt
```

It sends ServerChan alerts when the printer finishes, crashes, hits anti-collision, emergency-stops, or stops making print progress.

## Rules

- Print finished: push now.
- Collision / anti-collision / emergency stop: push now, repeat every 2 minutes until confirmed.
- Other errors / no progress: use the delay in the UI.
- PrintExp not running is fine. The monitor only reads logs.
- Optional watchdog can warn or launch `PrintExp.exe`.

## Use

1. Run `UVPrinterLogMonitor.exe`.
2. Choose the printer app folder.
3. Enter ServerChan SendKey or full `.send` URL.
4. Save.

## Build

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1
```

Output:

```text
dist\UVPrinterLogMonitor.exe
dist\UVPrinterLogMonitor_Stable_1.1.0.exe
```

## Do Not Commit

```text
printer_monitor_config.json
printer_monitor_debug.log
real printer logs
```

## Author

@kyroslee, 2026 Jun
