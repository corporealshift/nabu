package com.nabu.client.data

import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Upsert
import androidx.room.RoomDatabase
import androidx.room.Transaction
import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteDatabase
import kotlinx.coroutines.flow.Flow

/**
 * A session as this device knows it. [synced] is stored rather than derived,
 * because the app must be able to say it is behind while offline.
 */
@Entity(tableName = "sessions")
data class SessionRow(
    @PrimaryKey val id: String,
    val workspace: String = "",
    @ColumnInfo(name = "workspace_key") val workspaceKey: String = "",
    val state: String = "idle",
    val cursor: String = "",
    val synced: Boolean = false,
    @ColumnInfo(name = "updated_at") val updatedAt: Long = 0,
)

/** One mirrored event. [ordinal] preserves log order independently of the id. */
@Entity(
    tableName = "events",
    foreignKeys = [ForeignKey(
        entity = SessionRow::class,
        parentColumns = ["id"],
        childColumns = ["session_id"],
        onDelete = ForeignKey.CASCADE,
    )],
    indices = [Index("session_id"), Index(value = ["session_id", "ordinal"])],
)
data class EventRow(
    @PrimaryKey val id: String,
    @ColumnInfo(name = "session_id") val sessionId: String,
    val ordinal: Long,
    val type: String,
    /** The event verbatim, so a type this version cannot render is not lost. */
    val raw: String,
)

/**
 * A prompt composed on this device. It exists before any send is attempted,
 * and [clientId] is what makes retrying one safe.
 */
@Entity(tableName = "outbox", indices = [Index("session_id")])
data class OutboxRow(
    @PrimaryKey @ColumnInfo(name = "client_id") val clientId: String,
    @ColumnInfo(name = "session_id") val sessionId: String,
    val content: String,
    @ColumnInfo(name = "created_at") val createdAt: Long,
    /** The event the daemon assigned. Non-null means delivered. */
    @ColumnInfo(name = "event_id") val eventId: String? = null,
    @ColumnInfo(name = "last_error") val lastError: String? = null,
    /**
     * Set when the daemon refused in a way retrying cannot fix. A blocked row
     * is kept, because it holds something the user wrote, but it is taken out
     * of the send queue: left in, it would be retried ahead of every later
     * prompt forever and nothing would ever go.
     */
    @ColumnInfo(name = "blocked", defaultValue = "0") val blocked: Boolean = false,
)

@Dao
interface SessionDao {
    @Query("SELECT * FROM sessions ORDER BY updated_at DESC")
    fun watchAll(): Flow<List<SessionRow>>

    @Query("SELECT * FROM sessions WHERE id = :id")
    fun watch(id: String): Flow<SessionRow?>

    @Query("SELECT * FROM sessions WHERE id = :id")
    suspend fun get(id: String): SessionRow?

    // REPLACE would delete the existing row first, and the events' foreign key
    // cascades that delete, emptying the mirror on every relist.
    @Upsert
    suspend fun upsert(row: SessionRow)

    @Query("UPDATE sessions SET cursor = :cursor, synced = :synced, updated_at = :at WHERE id = :id")
    suspend fun markSynced(id: String, cursor: String, synced: Boolean, at: Long)

    @Query("UPDATE sessions SET state = :state WHERE id = :id")
    suspend fun setState(id: String, state: String)

    @Query("DELETE FROM sessions WHERE id = :id")
    suspend fun delete(id: String)
}

@Dao
interface EventDao {
    @Query("SELECT * FROM events WHERE session_id = :sessionId ORDER BY ordinal ASC")
    fun watch(sessionId: String): Flow<List<EventRow>>

    @Query("SELECT * FROM events WHERE session_id = :sessionId ORDER BY ordinal ASC")
    suspend fun all(sessionId: String): List<EventRow>

    @Query("SELECT COUNT(*) FROM events WHERE session_id = :sessionId")
    suspend fun count(sessionId: String): Int

    @Query("SELECT MAX(ordinal) FROM events WHERE session_id = :sessionId")
    suspend fun lastOrdinal(sessionId: String): Long?

    /**
     * The newest prompts. A long agent run can put dozens of assistant turns
     * between two prompts, so the role is matched in SQL rather than by taking
     * the last few messages; the caller confirms it after parsing.
     */
    @Query(
        "SELECT raw FROM events WHERE session_id = :sessionId AND type = 'message' " +
            "AND raw LIKE '%\"role\":\"user\"%' ORDER BY ordinal DESC LIMIT :limit"
    )
    suspend fun recentPrompts(sessionId: String, limit: Int = 5): List<String>

    /** An event is immutable, so a repeat is a no-op. */
    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insert(rows: List<EventRow>)
}

@Dao
interface OutboxDao {
    @Query("SELECT * FROM outbox WHERE event_id IS NULL AND blocked = 0 ORDER BY created_at ASC")
    fun watchPending(): Flow<List<OutboxRow>>

    @Query(
        "SELECT * FROM outbox WHERE session_id = :sessionId AND event_id IS NULL " +
            "AND blocked = 0 ORDER BY created_at ASC"
    )
    fun watchPendingFor(sessionId: String): Flow<List<OutboxRow>>

    @Query("SELECT * FROM outbox WHERE event_id IS NULL AND blocked = 0 ORDER BY created_at ASC")
    suspend fun pending(): List<OutboxRow>

    /**
     * Prompts the daemon refused for good. Watched app-wide rather than per
     * session: the row that jams the queue usually belongs to some other
     * session than the one being looked at, and a stuck queue that explains
     * itself nowhere is indistinguishable from a broken app.
     */
    @Query("SELECT * FROM outbox WHERE event_id IS NULL AND blocked = 1 ORDER BY created_at ASC")
    fun watchBlocked(): Flow<List<OutboxRow>>

    @Query("SELECT * FROM outbox WHERE client_id = :clientId")
    suspend fun byClientId(clientId: String): OutboxRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun put(row: OutboxRow)

    @Query("UPDATE outbox SET event_id = :eventId, last_error = NULL WHERE client_id = :clientId")
    suspend fun markSent(clientId: String, eventId: String)

    @Query("UPDATE outbox SET last_error = :error WHERE client_id = :clientId")
    suspend fun markFailed(clientId: String, error: String)

    @Query("UPDATE outbox SET blocked = 1, last_error = :error WHERE client_id = :clientId")
    suspend fun markBlocked(clientId: String, error: String)

    /** Puts a blocked prompt back in the queue, after the user fixed the cause. */
    @Query("UPDATE outbox SET blocked = 0, last_error = NULL WHERE client_id = :clientId")
    suspend fun unblock(clientId: String)

    @Query("DELETE FROM outbox WHERE client_id = :clientId")
    suspend fun discard(clientId: String)

    /**
     * Records why nothing is moving on every queued prompt, so each session's
     * banner can say it. Used when the failure is the connection rather than
     * any one prompt.
     */
    @Query("UPDATE outbox SET last_error = :error WHERE event_id IS NULL AND blocked = 0")
    suspend fun noteErrorOnPending(error: String)
}

/**
 * Adds `outbox.blocked`. Written out rather than left to the destructive
 * fallback because the outbox holds prompts the user typed and nothing else
 * has: the events and sessions would come back from the daemon on the next
 * sync, and an unsent prompt would not.
 */
val MIGRATION_1_2 = object : Migration(1, 2) {
    override fun migrate(db: SupportSQLiteDatabase) {
        db.execSQL("ALTER TABLE outbox ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0")
    }
}

@Database(
    entities = [SessionRow::class, EventRow::class, OutboxRow::class],
    version = 2,
    exportSchema = false,
)
abstract class MirrorDb : RoomDatabase() {
    abstract fun sessions(): SessionDao
    abstract fun events(): EventDao
    abstract fun outbox(): OutboxDao

    /** Appends and advances the cursor together, so the two cannot disagree. */
    @Transaction
    open suspend fun append(
        sessionId: String,
        rows: List<EventRow>,
        cursor: String,
        synced: Boolean,
        // When the session was last worked on. The caller passes the events'
        // own time: stamping the clock here would make catching up on an old
        // session look like activity.
        at: Long = System.currentTimeMillis(),
    ) {
        if (rows.isNotEmpty()) events().insert(rows)
        sessions().markSynced(sessionId, cursor, synced, at)
    }

    companion object {
        @Volatile private var instance: MirrorDb? = null

        /**
         * The mirror, once per process. The outbox worker runs in the same
         * process as the app, and two Room instances on one file disagree
         * about what is in flight.
         */
        fun get(context: android.content.Context): MirrorDb =
            instance ?: synchronized(this) {
                instance ?: androidx.room.Room
                    .databaseBuilder(
                        context.applicationContext,
                        MirrorDb::class.java,
                        "nabu-mirror",
                    )
                    .addMigrations(MIGRATION_1_2)
                    // Still the last resort for a version this build has no
                    // path from; the mirror is rebuildable from the daemon.
                    .fallbackToDestructiveMigration()
                    .build()
                    .also { instance = it }
            }
    }
}
