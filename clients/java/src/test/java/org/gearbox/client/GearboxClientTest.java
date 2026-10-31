package org.gearbox.client;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.nio.file.Path;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.junit.jupiter.api.io.TempDir;

class GearboxClientTest {
  static final String LEAD =
      "{\"lead\":{\"id\":\"L-1\",\"city\":\"Gurgaon\",\"source\":\"app\"},"
          + "\"car\":{\"make\":\"Maruti\",\"model\":\"Swift\",\"year\":2019,\"km\":48200,\"price\":525000},"
          + "\"inspections\":[{\"part\":\"engine\",\"score\":8}]}";

  @Test
  void fallsBackWhenSidecarIsMissing(@TempDir Path dir) {
    try (GearboxClient c = GearboxClient.connect("unix://" + dir.resolve("missing.sock"))) {
      long t0 = System.nanoTime();
      Decision d =
          c.decide("lead_routing", LEAD,
              DecideOptions.unit("L-1").timeout(Duration.ofMillis(5)).fallback("{\"route\":\"manual_review\"}"));
      assertTrue(d.fromClientFallback());
      assertEquals("{\"route\":\"manual_review\"}", d.outputJson());
      assertTrue(System.nanoTime() - t0 < 2_000_000_000L);
    }
  }

  /** Runs only with GEARBOX_TEST_SIDECAR=unix:///path (a sidecar serving examples/definitions). */
  @Test
  @EnabledIfEnvironmentVariable(named = "GEARBOX_TEST_SIDECAR", matches = ".+")
  void decidesAgainstRealSidecar() {
    try (GearboxClient c = GearboxClient.connect(System.getenv("GEARBOX_TEST_SIDECAR"))) {
      Decision d = c.decide("lead_routing", LEAD, DecideOptions.unit("L-1").timeout(Duration.ofSeconds(1)));
      assertFalse(d.fromClientFallback());
      assertTrue(d.version().startsWith("lead_routing@"), d.version());
      assertTrue(d.dependencyHash().startsWith("sha256:"));
      assertTrue(d.outputJson().contains("\"route\""));

      String leh = LEAD.replace("Gurgaon", "Leh");
      Decision r = c.decide("lead_routing", leh, DecideOptions.unit("L-2").timeout(Duration.ofSeconds(1)));
      assertTrue(r.outputJson().contains("city_not_served"), r.outputJson());

      StatusRuntimeException e =
          assertThrows(StatusRuntimeException.class,
              () -> c.decide("lead_routing", "{\"lead\":{}}",
                  DecideOptions.unit("x").timeout(Duration.ofSeconds(1)).fallback("{}")));
      assertEquals(Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());

      Decision nf = c.decide("no_such_decision", LEAD,
          DecideOptions.unit("x").timeout(Duration.ofSeconds(1)).fallback("{\"route\":\"manual_review\"}"));
      assertTrue(nf.fromClientFallback());
    }
  }
}
