package com.nabu.client.protocol

/**
 * JSON-RPC error codes, mirroring `protocol/errors.go` (spec 8).
 *
 * The client needs these because "the daemon said no" and "the daemon said no
 * and will say no every time" call for different handling: the first is worth
 * retrying, the second is not.
 */
object RpcCodes {
    const val PARSE_ERROR = -32700
    const val INVALID_REQUEST = -32600
    const val METHOD_NOT_FOUND = -32601
    const val INVALID_PARAMS = -32602
    const val INTERNAL_ERROR = -32603
    const val SESSION_NOT_FOUND = -32001
    const val INVALID_TRANSITION = -32002
    const val PERMISSION_DENIED = -32003
    const val ALREADY_RESOLVED = -32004
    const val CURSOR_UNKNOWN = -32005
    const val PROTOCOL_MISMATCH = -32006
    const val UNAUTHORIZED = -32007
    const val WORKSPACE_UNTRUSTED = -32008

    /**
     * Codes whose answer a retry cannot change, because they are a judgement
     * about the request rather than about the moment. A queued prompt that
     * gets one can never be delivered as it stands.
     *
     * `UNAUTHORIZED` and `PROTOCOL_MISMATCH` are deliberately absent: they are
     * about the connection, not this prompt, and they clear when the settings
     * or the binaries change. `INTERNAL_ERROR` is absent because the daemon
     * may simply have been unlucky.
     */
    val PERMANENT: Set<Int> = setOf(
        PARSE_ERROR,
        INVALID_REQUEST,
        METHOD_NOT_FOUND,
        INVALID_PARAMS,
        SESSION_NOT_FOUND,
        INVALID_TRANSITION,
        WORKSPACE_UNTRUSTED,
    )
}
