package com.nabu.client.net

import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** A stand-in daemon: answers any call, and pushes notifications on demand. */
class FakeDaemon {
    val server = MockWebServer()
    val received = ConcurrentLinkedQueue<String>()
    @Volatile var socket: WebSocket? = null
    val opened = CountDownLatch(1)

    /** Set to refuse the handshake, for the failure path. */
    @Volatile var refuseHandshake = false

    /** Prompts accepted, so a returned event id is unique per send. */
    val sent = java.util.concurrent.atomic.AtomicInteger(0)

    /**
     * Sessions whose prompts are refused, and the code to refuse with. This
     * is how a real daemon answers a prompt aimed at a session that has
     * ended: an error the same request will always get.
     */
    val refuseSession = java.util.concurrent.ConcurrentHashMap<String, Int>()

    /** Held open to prove a notification arrives while a call is in flight. */
    @Volatile var holdCall: CountDownLatch? = null

    fun start() {
        server.enqueue(MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
            override fun onOpen(ws: WebSocket, response: Response) {
                socket = ws
                opened.countDown()
            }

            override fun onMessage(ws: WebSocket, text: String) {
                received.add(text)
                val msg = com.nabu.client.protocol.NabuJson
                    .decodeFromString(Rpc.serializer(), text)
                val id = msg.id ?: return
                if (msg.method == "nabu.hello" && refuseHandshake) {
                    ws.send(errorFor(id, "protocol version mismatch"))
                    return
                }
                if (msg.method == "nabu.session.send_prompt") {
                    val session = msg.params?.let {
                        runCatching {
                            it.jsonObject["session_id"]?.jsonPrimitive?.content
                        }.getOrNull()
                    }
                    refuseSession[session]?.let { code ->
                        ws.send(errorFor(id, "session is completed; refused", code))
                        return
                    }
                }
                holdCall?.let {
                    // Answer only when the test says so.
                    Thread {
                        it.await(10, TimeUnit.SECONDS)
                        ws.send(resultFor(id, msg.method ?: ""))
                    }.start()
                    return
                }
                ws.send(resultFor(id, msg.method ?: ""))
            }
        }))
        server.start()
    }

    fun url(): String = server.url("/").toString()

    fun push(text: String) {
        requireNotNull(socket) { "no client connected" }.send(text)
    }

    fun stop() {
        socket?.close(1000, null)
        socket = null
        server.shutdown()
    }

    private fun resultFor(id: kotlinx.serialization.json.JsonElement, method: String) =
        if (method == "nabu.session.send_prompt")
            """{"jsonrpc":"2.0","id":${id},"result":{"event_id":"01SENT${sent.incrementAndGet()}"}}"""
        else
            """{"jsonrpc":"2.0","id":${id},"result":{"ok":true,"method":"$method"}}"""

    private fun errorFor(
        id: kotlinx.serialization.json.JsonElement,
        message: String,
        code: Int = -32000,
    ) = """{"jsonrpc":"2.0","id":${id},"error":{"code":$code,"message":"$message"}}"""
}
