package dev.daxiang.dxandroidstatus;

import android.Manifest;
import android.app.Activity;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

public class MainActivity extends Activity {
    private final Handler handler = new Handler(Looper.getMainLooper());
    private TextView status;
    private TextView details;
    private final BroadcastReceiver receiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            render(StatusService.latest());
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        requestNotificationPermission();
        StatusService.start(this);
        setContentView(buildView());
        render(StatusService.latest());
        refreshNow();
    }

    @Override
    protected void onResume() {
        super.onResume();
        IntentFilter filter = new IntentFilter(StatusService.ACTION_REFRESHED);
        if (Build.VERSION.SDK_INT >= 33) {
            registerReceiver(receiver, filter, RECEIVER_NOT_EXPORTED);
        } else {
            registerReceiver(receiver, filter);
        }
        refreshNow();
    }

    @Override
    protected void onPause() {
        unregisterReceiver(receiver);
        super.onPause();
    }

    private View buildView() {
        int pad = dp(18);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(pad, pad, pad, pad);
        root.setBackgroundColor(0xFFF8FAF9);

        TextView title = new TextView(this);
        title.setText("Daxiang VPN 出口");
        title.setTextSize(24);
        title.setTextColor(0xFF10201D);
        title.setGravity(Gravity.START);
        root.addView(title);

        status = new TextView(this);
        status.setTextSize(18);
        status.setTextColor(0xFF0F766E);
        status.setPadding(0, dp(12), 0, dp(10));
        root.addView(status);

        Button refresh = new Button(this);
        refresh.setText("刷新状态");
        refresh.setAllCaps(false);
        refresh.setOnClickListener(v -> refreshNow());
        root.addView(refresh);

        details = new TextView(this);
        details.setTextSize(14);
        details.setTextColor(0xFF24302D);
        details.setTextIsSelectable(true);
        details.setPadding(0, dp(14), 0, 0);

        ScrollView scroll = new ScrollView(this);
        scroll.addView(details);
        root.addView(scroll, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1
        ));

        return root;
    }

    private void refreshNow() {
        status.setText("刷新中...");
        new Thread(() -> {
            StatusSnapshot snapshot = StatusProbe.collect();
            handler.post(() -> render(snapshot));
        }).start();
    }

    private void render(StatusSnapshot s) {
        if (s == null) {
            status.setText("等待状态...");
            details.setText("");
            return;
        }
        status.setText(s.summary() + "  " + s.checkedAt);
        details.setText(
                "Root\n" + (s.rootOk ? "OK" : "不可用") + "\n\n"
                        + "进程\n" + (s.egressRunning ? s.pid : "未运行") + "\n\n"
                        + "公网路由\n" + empty(s.route) + "\n\n"
                        + "WireGuard\n" + empty(s.wg) + "\n\n"
                        + "反连会话\n" + empty(s.listen) + "\n\n"
                        + "守护日志\n" + empty(s.serviceLog) + "\n\n"
                        + "出口日志\n" + empty(s.egressLog)
        );
    }

    private void requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && !StatusService.hasNotificationPermission(this)) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
        }
    }

    private int dp(int value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }

    private static String empty(String value) {
        if (value == null || value.trim().isEmpty()) {
            return "无";
        }
        return value.trim();
    }
}
