package com.nabu.client.ui

import android.annotation.SuppressLint
import android.webkit.WebView
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.nabu.client.protocol.ToolCallData
import com.nabu.client.ui.theme.NabuTheme
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/** The tool the daemon's artifact module provides. */
const val ARTIFACT_TOOL = "artifact"

/** A call to the artifact tool as a transcript line, or null for any other call. */
fun artifactOf(key: String, d: ToolCallData): Line.Artifact? {
    if (d.tool != ARTIFACT_TOOL) return null
    val args = runCatching { d.arguments.jsonObject }.getOrNull() ?: return null
    val name = args["name"]?.jsonPrimitive?.content.orEmpty()
    val html = args["html"]?.jsonPrimitive?.content.orEmpty()
    if (name.isBlank() || html.isBlank()) return null
    val title = args["title"]?.jsonPrimitive?.content.orEmpty().ifBlank { name }
    return Line.Artifact(key, name, title, html)
}

/**
 * The policy a page opens under, the daemon's artifact.Sandbox: scripts and
 * styles in the page run, and nothing may be loaded or connected to.
 */
const val ARTIFACT_SANDBOX = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
    "img-src data: blob:; font-src data:; media-src data: blob:"

/**
 * The page with the sandbox first in the document, the daemon's artifact.Wrap:
 * put in the page's own head it would come after any script placed before it.
 */
fun artifactPage(html: String): String {
    val meta = """<meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="$ARTIFACT_SANDBOX">"""
    val body = html.removePrefix("\uFEFF").trimStart()
    if (body.length >= 9 && body.substring(0, 9).equals("<!doctype", ignoreCase = true)) {
        val end = body.indexOf('>')
        if (end >= 0) return body.substring(0, end + 1) + meta + body.substring(end + 1)
    }
    return "<!doctype html>$meta$body"
}

/** How a line opens its page; provided once at the top of the app. */
val LocalOpenArtifact = staticCompositionLocalOf<(Line.Artifact) -> Unit> { {} }

/** A page in the transcript: its title, and a way in. */
@Composable
fun ArtifactLine(line: Line.Artifact) {
    val c = NabuTheme.colors
    val open = LocalOpenArtifact.current
    Row(
        Modifier
            .fillMaxWidth()
            .background(c.surface, RoundedCornerShape(12.dp))
            .padding(start = 14.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("The agent made a page", style = MaterialTheme.typography.labelMedium, color = c.muted)
            Text(
                line.title,
                style = MaterialTheme.typography.titleSmall,
                color = c.ink,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
        TextButton(onClick = { open(line) }) { Text("Open") }
    }
}

/**
 * The page itself (issue 41). It is HTML the model wrote, and the model reads
 * pages it did not write, so it runs sandboxed: JavaScript for interactivity,
 * no network, no files, no bridge back into the app.
 */
@OptIn(ExperimentalMaterial3Api::class)
@SuppressLint("SetJavaScriptEnabled")
@Composable
fun ArtifactScreen(line: Line.Artifact, onBack: () -> Unit) {
    val c = NabuTheme.colors
    Scaffold(containerColor = c.background, topBar = {
        TopAppBar(
            colors = TopAppBarDefaults.topAppBarColors(containerColor = c.background, titleContentColor = c.ink),
            title = {
                Text(line.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } },
        )
    }) { padding ->
        // Loaded once per page. Any event in any session recomposes the app,
        // and loading in update would reset the page each time.
        key(line.key) {
            AndroidView(
                modifier = Modifier.fillMaxSize().padding(padding),
                factory = { ctx ->
                    WebView(ctx).apply {
                        settings.javaScriptEnabled = true
                        settings.blockNetworkLoads = true
                        settings.allowFileAccess = false
                        settings.allowContentAccess = false
                        settings.domStorageEnabled = false
                        loadDataWithBaseURL(null, artifactPage(line.html), "text/html", "utf-8", null)
                    }
                },
            )
        }
    }
}
