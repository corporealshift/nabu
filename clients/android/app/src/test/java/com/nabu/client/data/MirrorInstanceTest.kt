package com.nabu.client.data

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertNotSame
import org.junit.Assert.assertSame
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

/**
 * Seen on the emulator: leaving the app with back closed the process-wide
 * mirror, and reopening it in the same process got the closed instance, so
 * every connection failed with "the connection pool has been closed".
 */
@RunWith(RobolectricTestRunner::class)
class MirrorInstanceTest {
    @Test
    fun `a closed mirror is replaced, not handed out again`() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val first = MirrorDb.get(ctx)
        first.openHelper.writableDatabase // open it, as using it would
        first.close()

        // Robolectric reopens a closed SQLite connection on its own, which a
        // device does not, so the check is on what get hands out.
        val next = MirrorDb.get(ctx)
        assertNotSame("a closed mirror was handed out again", first, next)
        assertSame("an instance in use must be handed out again, not replaced", next, MirrorDb.get(ctx))
        next.close()
    }
}
