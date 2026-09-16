package com.nabu.client.settings

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import com.nabu.client.ui.theme.Mode
import com.nabu.client.ui.theme.Scheme
import kotlinx.coroutines.flow.map

/**
 * Where the daemon is and how to prove you may talk to it. The token is not
 * optional: a phone is never loopback, and the daemon refuses those without one.
 */
data class Settings(
    val host: String = "",
    val port: Int = 8737,
    val token: String = "",
    val scheme: Scheme = Scheme.Verdigris,
    val mode: Mode = Mode.System,
)

private val Context.store by preferencesDataStore(name = "nabu-settings")

class SettingsStore(private val context: Context) {
    private val hostKey = stringPreferencesKey("host")
    private val portKey = stringPreferencesKey("port")
    private val tokenKey = stringPreferencesKey("token")
    private val schemeKey = stringPreferencesKey("scheme")
    private val modeKey = stringPreferencesKey("mode")

    val settings: Flow<Settings> = context.store.data.map { p ->
        Settings(
            host = p[hostKey].orEmpty(),
            port = p[portKey]?.toIntOrNull() ?: 8737,
            token = p[tokenKey].orEmpty(),
            scheme = p[schemeKey]?.let { runCatching { Scheme.valueOf(it) }.getOrNull() }
                ?: Scheme.Verdigris,
            mode = p[modeKey]?.let { runCatching { Mode.valueOf(it) }.getOrNull() }
                ?: Mode.System,
        )
    }

    /** Appearance saves on its own so a tap applies without touching the connection. */
    suspend fun saveAppearance(scheme: Scheme, mode: Mode) {
        context.store.edit { p ->
            p[schemeKey] = scheme.name
            p[modeKey] = mode.name
        }
    }

    suspend fun save(s: Settings) {
        context.store.edit { p ->
            p[hostKey] = s.host.trim()
            p[portKey] = s.port.toString()
            p[tokenKey] = s.token.trim()
            p[schemeKey] = s.scheme.name
            p[modeKey] = s.mode.name
        }
    }
}
