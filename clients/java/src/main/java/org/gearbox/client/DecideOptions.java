package org.gearbox.client;

import java.time.Duration;

/** Per-call options. Immutable; each method returns a copy. */
public final class DecideOptions {
  final String unitId;
  final String requestId;
  final Duration timeout;
  final String fallbackJson;

  private DecideOptions(String unitId, String requestId, Duration timeout, String fallbackJson) {
    this.unitId = unitId;
    this.requestId = requestId;
    this.timeout = timeout;
    this.fallbackJson = fallbackJson;
  }

  public static DecideOptions defaults() {
    return new DecideOptions("", null, Duration.ofMillis(5), null);
  }

  public static DecideOptions unit(String unitId) {
    return defaults().withUnit(unitId);
  }

  public DecideOptions withUnit(String unitId) {
    return new DecideOptions(unitId, requestId, timeout, fallbackJson);
  }

  public DecideOptions requestId(String requestId) {
    return new DecideOptions(unitId, requestId, timeout, fallbackJson);
  }

  public DecideOptions timeout(Duration timeout) {
    return new DecideOptions(unitId, requestId, timeout, fallbackJson);
  }

  /** JSON returned (with fromClientFallback=true) when the sidecar cannot answer. */
  public DecideOptions fallback(String fallbackJson) {
    return new DecideOptions(unitId, requestId, timeout, fallbackJson);
  }
}
