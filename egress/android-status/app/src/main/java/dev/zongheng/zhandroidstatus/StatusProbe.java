package dev.zongheng.zhandroidstatus;

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

        String output = run(String.join("\n",
                "echo __ZH_ROOT__",
                "id",
                "echo __ZH_PID__",
                "pidof zhreverse 2>/dev/null || pgrep -f 'zhreverse client' 2>/dev/null || true",
                "echo __ZH_ROUTE__",
                "ip route get 8.8.8.8 2>&1 | head -1",
                "echo __ZH_LISTEN__",
                "ss -untp 2>/dev/null | grep zhreverse || true",
                "echo __ZH_WG__",
                "ip -4 addr 2>/dev/null | grep '10.66.0.101' || true",
                "echo __ZH_SERVICE_LOG__",
                "tail -8 /data/local/tmp/zhandroid-control.log 2>/dev/null || true",
                "echo __ZH_EGRESS_LOG__",
                "tail -14 /data/local/tmp/zhreverse-egress.log 2>/dev/null || true"
        ));

        String root = section(output, "__ZH_ROOT__", "__ZH_PID__");
        s.rootOk = root.contains("uid=0");
        if (!s.rootOk) {
            s.egressLog = output.trim();
            return s;
        }

        s.pid = firstLine(section(output, "__ZH_PID__", "__ZH_ROUTE__"));
        s.egressRunning = !s.pid.isEmpty();
        s.route = section(output, "__ZH_ROUTE__", "__ZH_LISTEN__");
        s.listen = section(output, "__ZH_LISTEN__", "__ZH_WG__");
        s.wg = section(output, "__ZH_WG__", "__ZH_SERVICE_LOG__");
        s.serviceLog = section(output, "__ZH_SERVICE_LOG__", "__ZH_EGRESS_LOG__");
        s.egressLog = sectionAfter(output, "__ZH_EGRESS_LOG__");
        return s;
    }

    private static String firstLine(String value) {
        int idx = value.indexOf('\n');
        if (idx >= 0) {
            value = value.substring(0, idx);
        }
        return value.trim();
    }

    private static String section(String output, String start, String end) {
        int startIdx = output.indexOf(start);
        if (startIdx < 0) {
            return "";
        }
        startIdx += start.length();
        int endIdx = output.indexOf(end, startIdx);
        if (endIdx < 0) {
            endIdx = output.length();
        }
        return output.substring(startIdx, endIdx).trim();
    }

    private static String sectionAfter(String output, String start) {
        int startIdx = output.indexOf(start);
        if (startIdx < 0) {
            return "";
        }
        return output.substring(startIdx + start.length()).trim();
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
