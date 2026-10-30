package org.gearbox.client;

/**
 * Result of a decide call.
 *
 * @param outputJson         the decision output as JSON text
 * @param version            e.g. "lead_routing@17"; log it with your business event
 * @param dependencyHash     hash of every version the decision read (for replay)
 * @param generation         bundle generation that answered
 * @param fallbackUsed       the decision's own fallback produced the output
 * @param fromClientFallback the sidecar could not answer; outputJson is the caller's fallback
 * @param requestId          request ID sent or generated
 */
public record Decision(
    String outputJson,
    String version,
    String dependencyHash,
    long generation,
    boolean fallbackUsed,
    boolean fromClientFallback,
    String requestId) {}
