package com.nabu.client.ui

import com.nabu.client.net.PermissionRequest
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Spec 15: the safe answer is always easier than the dangerous one. A phone in
 * a pocket must not be able to approve a command against the owner's repos.
 */
class PermissionTest {

    private fun request(risk: String) = PermissionRequest(
        id = JsonPrimitive(1),
        requestId = "r1",
        tool = "bash",
        summary = "rm -rf build",
        risk = risk,
    )

    @Test
    fun `high risk is the only one that needs holding`() {
        assertTrue(needsHold(request("high")))
        assertFalse(needsHold(request("medium")))
        assertFalse(needsHold(request("low")))
    }

    @Test
    fun `risk is matched whatever its case`() {
        assertTrue(needsHold(request("HIGH")))
        assertTrue(needsHold(request("High")))
    }

    @Test
    fun `an unknown risk is treated as the dangerous one`() {
        assertTrue("an unrecognised risk must not be the easy path", needsHold(request("")))
        assertTrue(needsHold(request("catastrophic")))
    }

    /** Approving needs the summary read; denying never does. */
    @Test
    fun `a high risk approval waits for the summary to be read`() {
        assertFalse(canApprove(request("high"), readToEnd = false))
        assertTrue(canApprove(request("high"), readToEnd = true))
    }

    @Test
    fun `a low risk approval does not`() {
        assertTrue(canApprove(request("low"), readToEnd = false))
    }

    /** Spec 7.15: the daemon reads a verdict, not a boolean. */
    @Test
    fun `the answer is a verdict`() {
        assertEquals("""{"verdict":"approve"}""", permissionReply(true).toString())
        assertEquals("""{"verdict":"deny"}""", permissionReply(false).toString())
    }

    /** The field name lives in the daemon; drifting from it silently denies. */
    @Test
    fun `the verdict field matches the one the daemon reads`() {
        var dir: java.io.File? = java.io.File("").absoluteFile
        while (dir != null && !java.io.File(dir, "daemon/api/requests.go").isFile) dir = dir.parentFile
        val go = java.io.File(requireNotNull(dir), "daemon/api/requests.go").readText()
        val reply = go.substringAfter("type permissionReply struct").substringBefore("}")

        assertTrue("the daemon no longer reads a verdict field: $reply", reply.contains("json:\"verdict\""))
    }

    @Test
    fun `the control says which gesture it wants`() {
        assertEquals("Hold to approve", approveLabel(request("high"), readToEnd = true))
        assertEquals("Read it first", approveLabel(request("high"), readToEnd = false))
        assertEquals("Approve", approveLabel(request("low"), readToEnd = false))
    }
}
