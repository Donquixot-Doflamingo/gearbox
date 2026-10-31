package org.gearbox.client;

import com.google.protobuf.ByteString;
import io.grpc.ManagedChannel;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.netty.NettyChannelBuilder;
import io.grpc.stub.MetadataUtils;
import io.netty.channel.EventLoopGroup;
import io.netty.channel.epoll.Epoll;
import io.netty.channel.epoll.EpollDomainSocketChannel;
import io.netty.channel.epoll.EpollEventLoopGroup;
import io.netty.channel.kqueue.KQueue;
import io.netty.channel.kqueue.KQueueDomainSocketChannel;
import io.netty.channel.kqueue.KQueueEventLoopGroup;
import io.netty.channel.unix.DomainSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.EnumSet;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import org.gearbox.v1.CheckGateRequest;
import org.gearbox.v1.DecideRequest;
import org.gearbox.v1.DecideResponse;
import org.gearbox.v1.EngineServiceGrpc;

/**
 * Thin client for the Gearbox sidecar: one channel, a deadline per call, a fallback value and a
 * request ID. Create one per process and reuse it.
 *
 * <pre>{@code
 * GearboxClient gearbox = GearboxClient.connect(System.getenv("GEARBOX_TARGET"));
 * Decision d = gearbox.decide("lead_routing", leadJson,
 *     DecideOptions.unit(leadId).timeout(Duration.ofMillis(5)).fallback("{\"route\":\"manual_review\"}"));
 * }</pre>
 */
public final class GearboxClient implements AutoCloseable {
  public static final String DEFAULT_TARGET = "unix:///var/run/gearbox/engine.sock";
  private static final Metadata.Key<String> REQUEST_ID =
      Metadata.Key.of("x-request-id", Metadata.ASCII_STRING_MARSHALLER);

  /**
   * Only caller errors (bad input) are thrown when a fallback is given. Every other failure —
   * sidecar missing or restarting, deadline, unusable socket (UNKNOWN), NOT_READY,
   * DECISION_NOT_FOUND, NODE_ERROR — means the sidecar could not answer: return the fallback.
   */
  private static final Set<Status.Code> CALLER_ERRORS = EnumSet.of(Status.Code.INVALID_ARGUMENT);

  private static boolean useFallback(Status.Code code) {
    return !CALLER_ERRORS.contains(code);
  }

  private final ManagedChannel channel;
  private final EventLoopGroup group;
  private final EngineServiceGrpc.EngineServiceBlockingStub stub;

  private GearboxClient(ManagedChannel channel, EventLoopGroup group) {
    this.channel = channel;
    this.group = group;
    this.stub = EngineServiceGrpc.newBlockingStub(channel);
  }

  /**
   * Connects to {@code unix:///path} (Linux: epoll, macOS: kqueue) or {@code host:port} (loopback
   * TCP).
   */
  public static GearboxClient connect(String target) {
    String t = (target == null || target.isEmpty()) ? DEFAULT_TARGET : target;
    if (t.startsWith("unix://")) {
      DomainSocketAddress addr = new DomainSocketAddress(t.substring("unix://".length()));
      if (Epoll.isAvailable()) {
        EventLoopGroup group = new EpollEventLoopGroup(1);
        return new GearboxClient(
            NettyChannelBuilder.forAddress(addr)
                .eventLoopGroup(group)
                .channelType(EpollDomainSocketChannel.class)
                .usePlaintext()
                .build(),
            group);
      }
      if (KQueue.isAvailable()) {
        EventLoopGroup group = new KQueueEventLoopGroup(1);
        return new GearboxClient(
            NettyChannelBuilder.forAddress(addr)
                .eventLoopGroup(group)
                .channelType(KQueueDomainSocketChannel.class)
                .usePlaintext()
                .build(),
            group);
      }
      throw new IllegalStateException(
          "Unix sockets need Netty epoll (Linux) or kqueue (macOS); use a host:port target instead");
    }
    return new GearboxClient(NettyChannelBuilder.forTarget(t).usePlaintext().build(), null);
  }

  /** Evaluates one decision. Throws {@link StatusRuntimeException} for caller errors (INPUT_SCHEMA). */
  public Decision decide(String decision, String inputJson, DecideOptions opts) {
    DecideOptions o = opts == null ? DecideOptions.defaults() : opts;
    String rid = o.requestId != null ? o.requestId : UUID.randomUUID().toString();
    Metadata md = new Metadata();
    md.put(REQUEST_ID, rid);
    DecideRequest req =
        DecideRequest.newBuilder()
            .setDecision(decision)
            .setInputJson(ByteString.copyFrom(inputJson, StandardCharsets.UTF_8))
            .setRequestId(rid)
            .setUnitId(o.unitId == null ? "" : o.unitId)
            .build();
    try {
      DecideResponse r =
          stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(md))
              .withDeadlineAfter(o.timeout.toNanos(), TimeUnit.NANOSECONDS)
              .decide(req);
      return new Decision(
          r.getOutputJson().toStringUtf8(),
          r.getDecisionVersion(),
          r.getDependencyHash(),
          r.getBundleGeneration(),
          r.getFallbackUsed(),
          false,
          r.getRequestId());
    } catch (StatusRuntimeException e) {
      if (o.fallbackJson != null && useFallback(e.getStatus().getCode())) {
        return new Decision(o.fallbackJson, "", "", 0, false, true, rid);
      }
      throw e;
    }
  }

  /** Evaluates a gate; returns {@code fallback} when the sidecar cannot answer. */
  public boolean checkGate(String gate, String unitId, String attributesJson, boolean fallback) {
    try {
      return stub.withDeadlineAfter(5, TimeUnit.MILLISECONDS)
          .checkGate(
              CheckGateRequest.newBuilder()
                  .setGate(gate)
                  .setUnitId(unitId == null ? "" : unitId)
                  .setAttributesJson(
                      ByteString.copyFrom(
                          attributesJson == null ? "{}" : attributesJson, StandardCharsets.UTF_8))
                  .build())
          .getValue();
    } catch (StatusRuntimeException e) {
      if (useFallback(e.getStatus().getCode())) {
        return fallback;
      }
      throw e;
    }
  }

  @Override
  public void close() {
    channel.shutdown();
    try {
      channel.awaitTermination(2, TimeUnit.SECONDS);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
    } finally {
      if (group != null) {
        group.shutdownGracefully();
      }
    }
  }
}
