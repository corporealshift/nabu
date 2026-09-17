package com.nabu.client.net

import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.Response
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

class DaemonClientTest {

    private lateinit var daemon: FakeDaemon

    @Before
    fun start() {
        daemon = FakeDaemon()
        daemon.start()
    }

    @After
    fun stop() = daemon.stop()

    /** Closes both ends so MockWebServer can actually shut down. */
    private fun DaemonClient.done() {
        close()
        daemon.socket?.close(1000, null)
    }

    private fun client(token: String = "sekrit") =
        DaemonClient(baseUrl = daemon.url(), token = token)

    @Test
    fun `the handshake sends the client name and protocol version`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        val hello = daemon.received.first { it.contains("nabu.hello") }
        assertTrue("hello did not carry the client name: $hello", hello.contains("nabu-android"))
        assertTrue("hello did not carry the protocol version: $hello",
            hello.contains("protocol_version"))
        c.done()
    }

    /** The token is what the daemon requires from anything but loopback. */
    @Test
    fun `the token is sent as a bearer header`() = runBlocking {
        val c = client(token = "sekrit")
        withTimeout(10_000) { c.connect() }

        val upgrade = daemon.server.takeRequest(5, TimeUnit.SECONDS)
        assertEquals("Bearer sekrit", upgrade?.getHeader("Authorization"))
        c.done()
    }

    /** A refused handshake must fail loudly rather than hang or half-connect. */
    @Test
    fun `a refused handshake throws`() = runBlocking {
        daemon.refuseHandshake = true
        val c = client()

        val thrown = try {
            withTimeout(10_000) { c.connect() }
            null
        } catch (e: DaemonException) {
            e
        }
        assertNotNull("connect should have thrown", thrown)
        assertTrue(thrown!!.message!!.contains("handshake refused"))
    }

    @Test
    fun `a call returns its own response`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        val r = withTimeout(10_000) { c.call("nabu.session.list") }
        assertEquals("nabu.session.list",
            r.result!!.jsonObject["method"]!!.jsonPrimitive.content)
        c.done()
    }

    /** The failure the Go client was rebuilt for: a call blocking the stream. */
    /** Without this the reconnect loop has nothing to wait on and never runs. */
    @Test
    fun `a dropped connection can be waited for`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        daemon.socket?.close(1000, "going away")

        val reason = withTimeout(10_000) { c.awaitClosed() }
        assertTrue("no reason was given for the close", reason.isNotEmpty())
    }

    @Test
    fun `a notification arrives while a call is in flight`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        val gate = CountDownLatch(1)
        daemon.holdCall = gate

        val got = async {
            withTimeout(10_000) {
                c.incoming.first { it is Incoming.Event } as Incoming.Event
            }
        }
        val call = async { withTimeout(15_000) { c.call("nabu.session.state") } }

        // The call is unanswered on purpose.
        delay(200)
        daemon.push("""{"jsonrpc":"2.0","method":"nabu.session.event","params":{
            "session_id":"S1","event":{"id":"E1","type":"message",
            "data":{"role":"user","content":"hello"}}}}""")

        val event = got.await()
        assertEquals("S1", event.value.sessionId)
        assertEquals("message", event.value.event.type)

        gate.countDown()
        call.await()
        c.done()
    }

    /** Concurrent calls must not interleave on the wire or cross their answers. */
    @Test
    fun `concurrent calls each get their own response`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        val methods = listOf(
            "nabu.session.list", "nabu.session.state", "nabu.session.events_after",
            "nabu.session.subscribe", "nabu.daemon.status",
        )
        val results = withTimeout(20_000) {
            methods.map { m -> async { c.call(m) } }.awaitAll()
        }

        results.forEachIndexed { i, r ->
            assertEquals("call ${methods[i]} got the wrong answer",
                methods[i], r.result!!.jsonObject["method"]!!.jsonPrimitive.content)
        }
        c.done()
    }

    /** A permission prompt is a request, not a notification, and needs answering. */
    @Test
    fun `a permission request is classified and can be answered`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        val got = async {
            withTimeout(10_000) {
                c.incoming.first { it is Incoming.Permission } as Incoming.Permission
            }
        }
        delay(200)
        daemon.push("""{"jsonrpc":"2.0","id":"r1","method":"nabu.rpc.permission.request",
            "params":{"request_id":"r1","tool":"bash","summary":"rm -rf /etc","risk":"high"}}""")

        val req = got.await()
        assertEquals("bash", req.value.tool)
        assertEquals("high", req.value.risk)
        assertEquals("rm -rf /etc", req.value.summary)

        c.respond(req.value.id, buildJsonObject { put("approved", false) })

        var answer: String? = null
        repeat(100) {
            answer = daemon.received.firstOrNull { m -> m.contains("approved") }
            if (answer != null) return@repeat
            Thread.sleep(50)
        }
        assertNotNull("the answer never reached the daemon", answer)
        assertTrue(answer!!.contains("false"))
        c.done()
    }

    /** A frame this client cannot parse costs that frame, not the connection. */
    @Test
    fun `an unreadable frame does not kill the connection`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }

        daemon.push("this is not json at all")
        delay(200)

        val r = withTimeout(10_000) { c.call("nabu.session.list") }
        assertNotNull("the connection died on a bad frame", r.result)
        c.done()
    }

    /** Losing the connection must fail waiting calls rather than hang them. */
    @Test
    fun `a dropped connection fails calls in flight`() = runBlocking {
        val c = client()
        withTimeout(10_000) { c.connect() }
        daemon.holdCall = CountDownLatch(1) // never released

        val call = async { runCatching { c.call("nabu.session.state") } }
        delay(200)
        daemon.stop()

        val outcome = withTimeout(15_000) { call.await() }
        assertTrue("a call outliving its connection should fail", outcome.isFailure)
    }
}
