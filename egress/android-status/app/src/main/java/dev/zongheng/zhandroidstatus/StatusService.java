package dev.zongheng.zhandroidstatus;

import android.Manifest;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;

public class StatusService extends Service {
    static final String ACTION_REFRESHED = "dev.zongheng.zhandroidstatus.REFRESHED";
    private static final String CHANNEL_ID = "zhandroid_status";
    private static final int NOTIFICATION_ID = 660101;
    private static StatusSnapshot latest;

    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Runnable tick = new Runnable() {
        @Override
        public void run() {
            refresh();
            handler.postDelayed(this, 120_000);
        }
    };

    static void start(Context context) {
        Intent intent = new Intent(context, StatusService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(intent);
        } else {
            context.startService(intent);
        }
    }

    static StatusSnapshot latest() {
        return latest;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        ensureChannel();
        latest = StatusProbe.collect();
        startForeground(NOTIFICATION_ID, buildNotification(latest));
        handler.post(tick);
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        handler.removeCallbacks(tick);
        handler.post(tick);
        return START_STICKY;
    }

    @Override
    public void onDestroy() {
        handler.removeCallbacks(tick);
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    private void refresh() {
        new Thread(() -> {
            StatusSnapshot snapshot = StatusProbe.collect();
            latest = snapshot;
            NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
            nm.notify(NOTIFICATION_ID, buildNotification(snapshot));
            sendBroadcast(new Intent(ACTION_REFRESHED).setPackage(getPackageName()));
        }).start();
    }

    private Notification buildNotification(StatusSnapshot s) {
        Intent openIntent = new Intent(this, MainActivity.class);
        PendingIntent pendingIntent = PendingIntent.getActivity(
                this,
                0,
                openIntent,
                PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE
        );

        String route = compact(s.route);
        String wg = compact(s.wg);
        Notification.BigTextStyle style = new Notification.BigTextStyle()
                .bigText("状态: " + s.summary()
                        + "\n链路: " + route
                        + "\nWG: " + wg
                        + "\n刷新: " + s.checkedAt);

        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Notification.Builder(this, CHANNEL_ID)
                : new Notification.Builder(this);
        return builder
                .setSmallIcon(R.drawable.ic_stat_dx)
                .setContentTitle("Zongheng VPN 出口")
                .setContentText(s.summary())
                .setStyle(style)
                .setContentIntent(pendingIntent)
                .setOngoing(true)
                .setShowWhen(false)
                .build();
    }

    private void ensureChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return;
        }
        NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID,
                "出口状态",
                NotificationManager.IMPORTANCE_LOW
        );
        channel.setDescription("显示 zhreverse 是否正在运行");
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        nm.createNotificationChannel(channel);
    }

    private static String compact(String value) {
        value = value == null ? "" : value.trim().replace('\n', ' ');
        return value.isEmpty() ? "unknown" : value;
    }

    static boolean hasNotificationPermission(Context context) {
        if (Build.VERSION.SDK_INT < 33) {
            return true;
        }
        return context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
    }
}
