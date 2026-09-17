package com.nabu.client.ui

import com.nabu.client.data.EventRow
import com.nabu.client.protocol.MessageData
import com.nabu.client.protocol.SessionData
import com.nabu.client.protocol.payload

/**
 * How full the model's context is, as a fraction, or null when it cannot be
 * known.
 *
 * The window comes from the session event and the usage from the last assistant
 * message. Deliberately the last rather than the sum: every request carries the
 * whole conversation again, so adding them up would count the same tokens once
 * per turn and reach several hundred percent.
 *
 * Null rather than zero when the window was never configured — a percentage of
 * an unknown number is an invention, and the caller should say nothing.
 */
fun contextUsed(rows: List<EventRow>): Float? {
    var window = 0
    var lastInput = 0

    for (event in decode(rows)) {
        when (event.type) {
            "session" -> event.payload<SessionData>()?.let { window = it.contextWindow }
            "message" -> event.payload<MessageData>()?.let { message ->
                if (message.role == "assistant") {
                    message.usage?.let { lastInput = it.inputTokens }
                }
            }
        }
    }

    if (window <= 0 || lastInput <= 0) return null
    return lastInput.toFloat() / window.toFloat()
}
