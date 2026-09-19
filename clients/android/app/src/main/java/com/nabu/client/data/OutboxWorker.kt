package com.nabu.client.data

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.nabu.client.net.DaemonClient
import com.nabu.client.settings.Settings
import com.nabu.client.settings.SettingsStore
import kotlinx.coroutines.flow.first
import java.util.concurrent.TimeUnit

/**
 * Sends queued prompts when the network comes back, whether or not the app is
 * open. A prompt composed on the underground is the case this exists for: the
 * app will not be in the foreground when signal returns.
 */
class OutboxWorker(
    context: Context,
    params: WorkerParameters,
) : CoroutineWorker(context, params) {

    override suspend fun doWork(): Result {
        val settings = SettingsStore(applicationContext).settings.first()
        val repo = SessionRepository(MirrorDb.get(applicationContext))

        return when (drainOutbox(repo, settings, ::open)) {
            Drain.Sent, Drain.Nothing -> Result.success()
            Drain.Failed -> Result.retry()
        }
    }

    private suspend fun open(s: Settings) =
        DaemonClient(baseUrl = "http://${s.host}:${s.port}/", token = s.token)
            .also { it.connect() }

    companion object {
        private const val NAME = "nabu-outbox"

        /**
         * Asks for a drain once there is a network. One pending queue needs
         * one worker however many prompts were composed into it, so the work
         * is unique.
         *
         * REPLACE rather than KEEP: the backoff on a repeatedly failing job
         * grows to hours, and KEEP dropped every new request onto that stale
         * schedule. Composing a prompt is the clearest possible signal that
         * the user wants it sent now, so it starts the wait over.
         */
        fun schedule(context: Context) {
            val request = OneTimeWorkRequestBuilder<OutboxWorker>()
                .setConstraints(
                    Constraints.Builder()
                        .setRequiredNetworkType(NetworkType.CONNECTED)
                        .build()
                )
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
                .build()

            WorkManager.getInstance(context)
                .enqueueUniqueWork(NAME, ExistingWorkPolicy.REPLACE, request)
        }
    }
}
