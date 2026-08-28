import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

public final class PilotService {
    private final AtomicBoolean ready = new AtomicBoolean(true);
    private final CountDownLatch running = new CountDownLatch(1);
    private final ExecutorService executor = Executors.newCachedThreadPool();
    private final HttpServer server;

    private PilotService() throws IOException {
        server = HttpServer.create(new InetSocketAddress("0.0.0.0", 8080), 0);
        server.setExecutor(executor);
        server.createContext("/ready", this::readiness);
        server.createContext("/work", this::work);
    }

    public static void main(String[] args) throws Exception {
        PilotService service = new PilotService();
        Runtime.getRuntime().addShutdownHook(new Thread(service::shutdown, "graceful-shutdown"));
        service.server.start();
        System.out.println("Java pilot listening on port 8080");
        service.running.await();
    }

    private void readiness(HttpExchange exchange) throws IOException {
        send(exchange, ready.get() ? 200 : 503, new byte[0]);
    }

    private void work(HttpExchange exchange) throws IOException {
        int delay = queryDelay(exchange.getRequestURI().getRawQuery());
        try {
            Thread.sleep(delay);
            send(exchange, 200, "completed\n".getBytes(StandardCharsets.UTF_8));
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            send(exchange, 503, "interrupted\n".getBytes(StandardCharsets.UTF_8));
        }
    }

    private void shutdown() {
        System.out.println("received termination signal; draining requests");
        ready.set(false);
        try {
            Thread.sleep(100);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        }
        server.stop(10);
        executor.shutdown();
        try {
            if (!executor.awaitTermination(10, TimeUnit.SECONDS)) {
                executor.shutdownNow();
            }
        } catch (InterruptedException interrupted) {
            executor.shutdownNow();
            Thread.currentThread().interrupt();
        }
        System.out.println("graceful shutdown complete");
        running.countDown();
    }

    private static int queryDelay(String query) {
        if (query == null) {
            return 2000;
        }
        for (String part : query.split("&")) {
            if (!part.startsWith("delay_ms=")) {
                continue;
            }
            try {
                return Math.min(Math.max(Integer.parseInt(part.substring("delay_ms=".length())), 0), 30000);
            } catch (NumberFormatException ignored) {
                return 2000;
            }
        }
        return 2000;
    }

    private static void send(HttpExchange exchange, int status, byte[] body) throws IOException {
        if (body.length > 0) {
            exchange.getResponseHeaders().set("Content-Type", "text/plain");
        }
        exchange.sendResponseHeaders(status, body.length);
        if (body.length > 0) {
            exchange.getResponseBody().write(body);
        }
        exchange.close();
    }
}
