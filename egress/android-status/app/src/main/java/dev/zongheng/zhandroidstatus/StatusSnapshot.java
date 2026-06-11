package dev.zongheng.zhandroidstatus;

final class StatusSnapshot {
    boolean rootOk;
    boolean egressRunning;
    String pid = "";
    String route = "";
    String listen = "";
    String wg = "";
    String serviceLog = "";
    String egressLog = "";
    String checkedAt = "";

    String summary() {
        if (!rootOk) {
            return "root 不可用";
        }
        if (!egressRunning) {
            return "出口未运行";
        }
        return "运行中 pid=" + pid;
    }
}
