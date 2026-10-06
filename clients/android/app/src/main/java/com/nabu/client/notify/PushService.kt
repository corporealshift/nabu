package com.nabu.client.notify

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.nabu.client.MainActivity
import com.nabu.client.R
import com.nabu.client.data.MirrorDb
import com.nabu.client.data.SessionRepository
import com.nabu.client.ui.SessionCard
import com.nabu.client.ui.projectName
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.tasks.await

/** The extra a tapped notification opens the app with. */
const val EXTRA_SESSION_ID = "com.nabu.client.SESSION_ID"

/** Receives the daemon's pushes and shows them. */
class PushService : FirebaseMessagingService() {

    // A new token is registered on the next connect, which registers every
    // time; there is no connection here to send it on.
    override fun onNewToken(token: String) = Unit

    override fun onMessageReceived(message: RemoteMessage) {
        val push = Push.parse(message.data) ?: return
        val manager = NotificationManagerCompat.from(this)
        if (push.kind == Push.RESOLVED) {
            manager.cancel(notificationId(push))
            return
        }
        if (OnScreen.showing(push.sessionId)) return
        ensureChannels(this)

        // A data message is handled off the main thread, and briefly: the
        // mirror is local, so reading it here is quick.
        val (project, title) = runBlocking { describe(this@PushService, push.sessionId) }
        val words = wording(push, project, title)
        val open = PendingIntent.getActivity(
            this,
            push.sessionId.hashCode(),
            Intent(this, MainActivity::class.java)
                .putExtra(EXTRA_SESSION_ID, push.sessionId)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val notification = NotificationCompat.Builder(this, words.channel)
            .setSmallIcon(R.drawable.ic_notify)
            .setContentTitle(words.title)
            .setContentText(words.text)
            .setContentIntent(open)
            .setAutoCancel(true)
            .setPriority(
                if (words.channel == Channels.QUESTIONS) NotificationCompat.PRIORITY_HIGH
                else NotificationCompat.PRIORITY_DEFAULT,
            )
            .build()
        runCatching { manager.notify(notificationId(push), notification) } // refused without the permission
    }
}

/** The session's project and its title, from the mirror. */
private suspend fun describe(context: Context, sessionId: String): Pair<String, String> {
    val repo = SessionRepository(MirrorDb.get(context))
    val row = MirrorDb.get(context).sessions().get(sessionId) ?: return "" to ""
    val card = SessionCard(row, repo.latestPrompt(sessionId), repo.latestOptions(sessionId))
    return projectName(row.workspace) to card.title
}

/** Makes the two channels; Android keeps the first creation's settings. */
fun ensureChannels(context: Context) {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
    val manager = context.getSystemService(NotificationManager::class.java)
    manager.createNotificationChannel(
        NotificationChannel(Channels.QUESTIONS, "Questions", NotificationManager.IMPORTANCE_HIGH)
            .apply { description = "A session is waiting for your answer" },
    )
    manager.createNotificationChannel(
        NotificationChannel(Channels.RUNS, "Runs and sessions", NotificationManager.IMPORTANCE_DEFAULT)
            .apply { description = "A run or goal finished or stopped, or a long turn ended" },
    )
}

/** Whether this build can be pushed to: it was built with google-services.json. */
fun pushAvailable(context: Context): Boolean = FirebaseApp.getApps(context).isNotEmpty()

/** This phone's push token, or null when the build cannot be pushed to. */
suspend fun pushToken(context: Context): String? =
    if (!pushAvailable(context)) null else runCatching { FirebaseMessaging.getInstance().token.await() }.getOrNull()
