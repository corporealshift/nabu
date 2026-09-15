package com.nabu.client.protocol

import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.Json
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * The projection is normative (spec 5) and shared with the Go daemon, so the
 * same conformance vectors decide both. A Kotlin projection that merely looks
 * right is worth nothing: the phone would disagree with the daemon about what
 * a session is doing.
 */
class ProjectionTest {

    private val vectors: File by lazy {
        // Walk up from the module directory, because the unit test's working
        // directory is app/ and the vectors belong to the protocol package.
        var dir: File? = File("").absoluteFile
        while (dir != null && !File(dir, "protocol/vectors/projection").isDirectory) {
            dir = dir.parentFile
        }
        requireNotNull(dir) { "could not find protocol/vectors from ${File("").absolutePath}" }
        File(dir, "protocol/vectors/projection")
    }

    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    @Test
    fun `every projection vector agrees with the go implementation`() {
        val files = vectors.listFiles { f -> f.name.endsWith(".json") }?.sortedBy { it.name }
        assertTrue("no vectors found in $vectors", !files.isNullOrEmpty())

        for (file in files!!) {
            val vector = json.parseToJsonElement(file.readText()).jsonObject
            val log: List<Event> =
                NabuJson.decodeFromString(vector["input"]!!.toString())
            val got = project(log)
            val want = vector["expect"]!!.jsonObject
            val name = file.name

            want["state"]?.let { assertEquals("$name state", it.jsonPrimitive.content, got.state) }
            want["turns"]?.let {
                assertEquals("$name turns", it.jsonPrimitive.content.toInt(), got.turns)
            }
            want["last_event_id"]?.let {
                assertEquals("$name last_event_id", it.jsonPrimitive.content, got.lastEventId)
            }
            want["compacted_through"]?.let {
                assertEquals("$name compacted_through", it.jsonPrimitive.content, got.compactedThrough)
            }
            want["options"]?.let { assertOptions(name, it.jsonObject, got.options) }
            want["usage"]?.let { assertUsage(name, it.jsonObject, got.usage) }
            want["budget"]?.let { assertBudget(name, it.jsonObject, got.budget) }
            want["tasks"]?.let {
                assertEquals("$name task count", it.jsonArray.size, got.tasks.size)
                it.jsonArray.forEachIndexed { i, t ->
                    assertEquals("$name task $i id",
                        t.jsonObject["id"]!!.jsonPrimitive.content, got.tasks[i].id)
                    assertEquals("$name task $i status",
                        t.jsonObject["status"]!!.jsonPrimitive.content, got.tasks[i].status)
                }
            }
            want["goal"]?.let {
                val g = requireNotNull(got.goal) { "$name: expected a goal" }
                assertEquals("$name goal state", it.jsonObject["state"]!!.jsonPrimitive.content, g.state)
            }
        }
    }

    private fun assertOptions(name: String, want: JsonObject, got: Options) {
        want["model"]?.let { assertEquals("$name model", it.jsonPrimitive.content, got.model) }
        want["compaction_enabled"]?.let {
            assertEquals("$name compaction_enabled",
                it.jsonPrimitive.content.toBoolean(), got.compactionEnabled)
        }
        want["permission_mode"]?.let {
            assertEquals("$name permission_mode", it.jsonPrimitive.content, got.permissionMode)
        }
    }

    private fun assertUsage(name: String, want: JsonObject, got: Usage) {
        assertEquals("$name input_tokens",
            want["input_tokens"]?.jsonPrimitive?.content?.toInt() ?: 0, got.inputTokens)
        assertEquals("$name output_tokens",
            want["output_tokens"]?.jsonPrimitive?.content?.toInt() ?: 0, got.outputTokens)
        assertEquals("$name cached_tokens",
            want["cached_tokens"]?.jsonPrimitive?.content?.toInt() ?: 0, got.cachedTokens)
    }

    private fun assertBudget(name: String, want: JsonObject, got: BudgetData) {
        assertEquals("$name max_turns",
            want["max_turns"]?.jsonPrimitive?.content?.toInt() ?: 0, got.maxTurns)
        assertEquals("$name max_tokens",
            want["max_tokens"]?.jsonPrimitive?.content?.toInt() ?: 0, got.maxTokens)
    }

    /** A log with no state_change is idle, per spec 5. */
    @Test
    fun `an empty log is idle`() {
        assertEquals("idle", project(emptyList()).state)
    }

    /** An event type this client has never heard of must not break the fold. */
    @Test
    fun `an unknown event type is skipped rather than fatal`() {
        val log = listOf(
            Event(id = "01A", type = "message",
                data = NabuJson.parseToJsonElement("""{"role":"assistant","content":"hi"}""")),
            Event(id = "01B", type = "something_from_the_future",
                data = NabuJson.parseToJsonElement("""{"whatever":true}""")),
        )
        val got = project(log)
        assertEquals(1, got.turns)
        assertEquals("01B", got.lastEventId)
    }

    /** A payload that will not decode costs its own line and nothing else. */
    @Test
    fun `a malformed payload does not stop the projection`() {
        val log = listOf(
            Event(id = "01A", type = "state_change",
                data = NabuJson.parseToJsonElement("""{"to":"running"}""")),
            Event(id = "01B", type = "budget", data = NabuJson.parseToJsonElement("""[]""")),
            Event(id = "01C", type = "message",
                data = NabuJson.parseToJsonElement("""{"role":"assistant","content":"x"}""")),
        )
        val got = project(log)
        assertEquals("running", got.state)
        assertEquals(1, got.turns)
    }
}
