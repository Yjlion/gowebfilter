package com.webfilter.app

import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/**
 * Main-thread-safe view of the embedded engine's lifecycle.
 *
 * Mobile.start/stop run on [worker] because a start can take seconds (CA
 * generation, SQLite schema, binding listeners) and the Go side holds its
 * controller lock for the whole bring-up — so Mobile.isRunning()/status()
 * called from the main thread during a start would block it and trigger an
 * ANR. UI code reads these flags instead; the worker updates them.
 *
 * The worker is process-wide (not per service instance) so a stop queued by
 * a dying service instance is always ordered before a start queued by its
 * successor.
 */
object EngineState {
    val worker: ExecutorService = Executors.newSingleThreadExecutor { r ->
        Thread(r, "webfilter-engine")
    }

    /** Engine is up (as of the last completed start/stop). */
    @Volatile
    var running = false
        internal set

    /** A start or stop is in progress. */
    @Volatile
    var busy = false
        internal set

    /** Loopback mgmt URL while running, else "". */
    @Volatile
    var mgmtUrl = ""
        internal set
}
