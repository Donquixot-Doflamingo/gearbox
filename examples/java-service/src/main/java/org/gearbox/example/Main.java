package org.gearbox.example;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.grpc.StatusRuntimeException;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.Executors;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.gearbox.client.DecideOptions;
import org.gearbox.client.Decision;
import org.gearbox.client.GearboxClient;

/**
 * Example Java backend that asks the local Gearbox sidecar for decisions.
 *
 * <pre>
 *   java -jar target/java-service.jar                 serve  POST /route, GET /healthz (EXAMPLE_ADDR, default 127.0.0.1:8083)
 *   java -jar target/java-service.jar bench 5000 1,4,8   latency/throughput of Java client → sidecar
 * </pre>
 *
 * Env: GEARBOX_TARGET (default unix:///var/run/gearbox/engine.sock).
 */
public final class Main {
  static final String FALLBACK = "{\"route\":\"manual_review\",\"reason\":\"client_fallback\"}";
  static final Pattern LEAD_ID = Pattern.compile("\"lead\"\\s*:\\s*\\{[^}]*\"id\"\\s*:\\s*\"([^\"]*)\"");

  public static void main(String[] args) throws Exception {
    String target = System.getenv().getOrDefault("GEARBOX_TARGET", GearboxClient.DEFAULT_TARGET);
    if (args.length > 0 && args[0].equals("bench")) {
      bench(target, args.length > 1 ? Integer.parseInt(args[1]) : 5000, args.length > 2 ? args[2] : "1,4,8");
      return;
    }
    GearboxClient gearbox = GearboxClient.connect(target); // one client per process
    String addr = System.getenv().getOrDefault("EXAMPLE_ADDR", "127.0.0.1:8083");
    int colon = addr.lastIndexOf(':');
    HttpServer server =
        HttpServer.create(new InetSocketAddress(addr.substring(0, colon), Integer.parseInt(addr.substring(colon + 1))), 0);
    server.setExecutor(Executors.newFixedThreadPool(8));
    server.createContext("/healthz", ex -> send(ex, 200, "{\"ok\":true}"));
    server.createContext("/route", ex -> route(ex, gearbox));
    server.start();
    System.out.println("java-service listening on " + addr + ", sidecar " + target);
  }

  static void route(HttpExchange ex, GearboxClient gearbox) throws IOException {
    if (!ex.getRequestMethod().equals("POST")) {
      send(ex, 405, "{\"error\":\"POST only\"}");
      return;
    }
    String lead = new String(ex.getRequestBody().readAllBytes(), StandardCharsets.UTF_8);
    Matcher m = LEAD_ID.matcher(lead);
    String unit = m.find() ? m.group(1) : "";
    try {
      Decision d =
          gearbox.decide("lead_routing", lead,
              DecideOptions.unit(unit).timeout(Duration.ofMillis(5)).fallback(FALLBACK));
      String out = d.outputJson().trim();
      String body =
          out.substring(0, out.length() - 1)
              + String.format(
                  ",\"version\":\"%s\",\"dependencyHash\":\"%s\",\"generation\":%d,\"fallbackUsed\":%b,\"fromClientFallback\":%b,\"service\":\"java\"}",
                  d.version(), d.dependencyHash(), d.generation(), d.fallbackUsed(), d.fromClientFallback());
      System.out.printf("route version=%s generation=%d fallback=%b%n", d.version(), d.generation(), d.fromClientFallback());
      send(ex, 200, body);
    } catch (StatusRuntimeException e) { // caller error, e.g. input does not match lead_input
      send(ex, 422, "{\"error\":\"" + e.getStatus().getCode() + "\"}");
    }
  }

  static void send(HttpExchange ex, int code, String body) throws IOException {
    byte[] raw = body.getBytes(StandardCharsets.UTF_8);
    ex.getResponseHeaders().set("content-type", "application/json");
    ex.sendResponseHeaders(code, raw.length);
    try (OutputStream os = ex.getResponseBody()) {
      os.write(raw);
    }
  }

  static final String LEAD =
      "{\"lead\":{\"id\":\"L-77\",\"city\":\"Indore\",\"source\":\"web\"},\"car\":{\"make\":\"Hyundai\",\"model\":\"i20\","
          + "\"year\":2021,\"km\":12000,\"price\":690000},\"inspections\":[{\"part\":\"engine\",\"score\":6}]}";

  /** Real measurement: N calls split over T threads, one shared client (as in a service). */
  static void bench(String target, int calls, String threadList) throws Exception {
    try (GearboxClient c = GearboxClient.connect(target)) {
      DecideOptions warm = DecideOptions.unit("warm").timeout(Duration.ofSeconds(2));
      for (int i = 0; i < 2000; i++) c.decide("lead_routing", LEAD, warm); // JIT warm-up
      for (String ts : threadList.split(",")) {
        int threads = Integer.parseInt(ts.trim());
        int per = calls / threads;
        List<Long> lat = Collections.synchronizedList(new ArrayList<>(calls));
        Thread[] pool = new Thread[threads];
        long t0 = System.nanoTime();
        for (int t = 0; t < threads; t++) {
          final int id = t;
          pool[t] =
              new Thread(
                  () -> {
                    List<Long> mine = new ArrayList<>(per);
                    for (int n = 0; n < per; n++) {
                      long s = System.nanoTime();
                      c.decide("lead_routing", LEAD, DecideOptions.unit("u" + id + "-" + n).timeout(Duration.ofSeconds(2)));
                      mine.add((System.nanoTime() - s) / 1000);
                    }
                    lat.addAll(mine);
                  });
          pool[t].start();
        }
        for (Thread t : pool) t.join();
        double wall = (System.nanoTime() - t0) / 1e9;
        List<Long> s = new ArrayList<>(lat);
        Collections.sort(s);
        System.out.printf(
            "{\"client\":\"java\",\"threads\":%d,\"calls\":%d,\"perMinute\":%d,\"p50Micros\":%d,\"p95Micros\":%d,\"p99Micros\":%d}%n",
            threads, s.size(), Math.round(s.size() / wall * 60), q(s, .50), q(s, .95), q(s, .99));
      }
    }
  }

  static long q(List<Long> s, double p) {
    return s.get((int) Math.min(s.size() - 1, Math.round(p * (s.size() - 1))));
  }
}
