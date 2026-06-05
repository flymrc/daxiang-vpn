package dev.daxiang.dxandroidstatus;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;

final class StatusProbe {
    private static final Pattern ANSI = Pattern.compile("\\u001B\\[[;\\d]*[ -/]*[@-~]");

    private StatusProbe() {
    }

    static StatusSnapshot collect() {
        StatusSnapshot s = new StatusSnapshot();
        s.checkedAt = new SimpleDateFormat("HH:mm:ss", Locale.JAPAN).format(new Date());

        String root = run("id");
        s.rootOk = root.contains("uid=0");
        if (!s.rootOk) {
            s.egressLog = root.trim();
            return s;
        }

        s.pid = firstLine(run("pidof dxandroid-egress || true"));
        s.egressRunning = !s.pid.isEmpty();
        s.route = run("ip route get 8.8.8.8 2>&1 | head -1");
        s.listen = run("ss -lntp 2>/dev/null | grep 10.66.0.101:1080 || true");
        s.wg = run("ip -4 addr show wg0 2>/dev/null | grep 'inet ' || true");
        s.serviceLog = run("tail -8 /data/local/tmp/dxandroid-egress-work/service.log 2>/dev/null || true");
        s.egressLog = run("tail -14 /data/local/tmp/dxandroid-egress-work/egress.log 2>/dev/null || true");
        return s;
    }

    private static String firstLine(String value) {
        int idx = value.indexOf('\n');
        if (idx >= 0) {
            value = value.substring(0, idx);
        }
        return value.trim();
    }

    private static String run(String command) {
        Process process = null;
        try {
            process = new ProcessBuilder("su", "-c", command)
                    .redirectErrorStream(true)
                    .start();
            StringBuilder out = new StringBuilder();
            try (BufferedReader reader = new BufferedReader(new InputStreamReader(process.getInputStream()))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    out.append(line).append('\n');
                }
            }
            if (!process.waitFor(5, TimeUnit.SECONDS)) {
                process.destroyForcibly();
                return "命令超时: " + command;
            }
            return ANSI.matcher(out.toString()).replaceAll("").trim();
        } catch (Exception e) {
            return e.getClass().getSimpleName() + ": " + e.getMessage();
        } finally {
            if (process != null) {
                process.destroy();
            }
        }
    }
}
