package com.gearworks.tesseract;


import java.io.*;
import java.net.Socket;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.util.UUID;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.atomic.AtomicBoolean;

public class TesseractServiceClient {
    private static final byte TYPE_HELLO       = 0x01;
    private static final byte TYPE_HELLO_ACK   = 0x02;
    private static final byte TYPE_SUBSCRIBE   = 0x03;
    private static final byte TYPE_UNSUBSCRIBE = 0x04;
    private static final byte TYPE_INV_PUSH    = 0x05;
    private static final byte TYPE_INV_UPDATE  = 0x06;
    private static final byte TYPE_INV_REQUEST = 0x07;
    private static final byte TYPE_INV_RESPONSE = 0x08;
    private static final byte TYPE_PING        = 0x09;
    private static final byte TYPE_PONG             = 0x0A;
    private static final byte TYPE_INV_PUSH_REJECT = 0x0B;
    private static final byte TYPE_INV_PUSH_ACK    = 0x0C;
    private static final byte TYPE_BATCH_OPS       = 0x0D;
    private static final byte TYPE_BATCH_RESULT    = 0x0E;

    private static final byte PROTOCOL_V2 = 0x02;

    private static final int MAX_PACKET_SIZE = 1024 * 1024;

    private final String host;
    private final int port;
    private final String serverName;
    private final BlockingQueue<byte[]> writeQueue = new LinkedBlockingQueue<>(1024);
    private final AtomicBoolean running = new AtomicBoolean(false);
    private final AtomicBoolean connected = new AtomicBoolean(false);

    private volatile Socket socket;
    private volatile DataInputStream dataIn;
    private volatile DataOutputStream dataOut;
    private PacketHandler handler;
    private final java.util.concurrent.ConcurrentHashMap<UUID, java.util.concurrent.CompletableFuture<BatchResult>> pendingResults = new java.util.concurrent.ConcurrentHashMap<>();

    public static final byte OP_INSERT  = 0x01;
    public static final byte OP_EXTRACT = 0x02;

    public static final byte RESULT_ACCEPTED              = 0x00;
    public static final byte RESULT_REJECTED_FULL         = 0x01;
    public static final byte RESULT_REJECTED_EMPTY        = 0x02;
    public static final byte RESULT_REJECTED_MISMATCH     = 0x03;
    public static final byte RESULT_REJECTED_INSUFFICIENT = 0x04;

    public record BatchOperation(byte type, int slot, int count, byte[] itemNbt) {}
    public record BatchResult(UUID owner, long timestamp, byte[] statuses, byte[] snapshot) {}

    public interface PacketHandler {
        void onInvUpdate(UUID owner, long timestamp, byte[] nbtData);
        void onInvResponse(UUID owner, boolean found, long timestamp, byte[] nbtData);
        void onInvPushReject(UUID owner, long timestamp, byte[] nbtData);
        void onInvPushAck(UUID owner, long timestamp);
        void onBatchResult(BatchResult result);
        void onConnected();
        void onDisconnected();
    }

    public TesseractServiceClient(String host, int port, String serverName) {
        this.host = host;
        this.port = port;
        this.serverName = serverName;
    }

    public void setHandler(PacketHandler handler) {
        this.handler = handler;
    }

    public void start() {
        if (running.getAndSet(true)) return;
        Thread thread = new Thread(this::connectionLoop, "tesseract-client");
        thread.setDaemon(true);
        thread.start();
    }

    public void stop() {
        running.set(false);
        closeSocket();
        writeQueue.clear();
    }

    public boolean isConnected() {
        return connected.get();
    }

    public void subscribe(UUID owner) {
        queueFrame(TYPE_SUBSCRIBE, uuidBytes(owner));
    }

    public void unsubscribe(UUID owner) {
        queueFrame(TYPE_UNSUBSCRIBE, uuidBytes(owner));
    }

    public void pushInventory(UUID owner, long timestamp, byte[] compressedNbt) {
        ByteBuffer buf = ByteBuffer.allocate(16 + 8 + compressedNbt.length);
        buf.putLong(owner.getMostSignificantBits());
        buf.putLong(owner.getLeastSignificantBits());
        buf.putLong(timestamp);
        buf.put(compressedNbt);
        queueFrame(TYPE_INV_PUSH, buf.array());
    }

    public void requestInventory(UUID owner) {
        queueFrame(TYPE_INV_REQUEST, uuidBytes(owner));
    }

    public void sendBatchOps(UUID owner, java.util.List<BatchOperation> ops) {
        int size = 16 + 2;
        for (BatchOperation op : ops) {
            size += 4;
            if (op.type() == OP_INSERT && op.itemNbt() != null) {
                size += 4 + op.itemNbt().length;
            }
        }
        ByteBuffer buf = ByteBuffer.allocate(size);
        buf.putLong(owner.getMostSignificantBits());
        buf.putLong(owner.getLeastSignificantBits());
        buf.putShort((short) ops.size());
        for (BatchOperation op : ops) {
            buf.put(op.type());
            buf.put((byte) op.slot());
            buf.putShort((short) op.count());
            if (op.type() == OP_INSERT && op.itemNbt() != null) {
                buf.putInt(op.itemNbt().length);
                buf.put(op.itemNbt());
            }
        }
        queueFrame(TYPE_BATCH_OPS, buf.array());
    }

    public BatchResult sendBatchOpsSync(UUID owner, java.util.List<BatchOperation> ops, long timeoutMs) {
        if (!connected.get()) return null;
        var future = new java.util.concurrent.CompletableFuture<BatchResult>();
        pendingResults.put(owner, future);
        sendBatchOps(owner, ops);
        try {
            return future.get(timeoutMs, java.util.concurrent.TimeUnit.MILLISECONDS);
        } catch (Exception e) {
            pendingResults.remove(owner);
            return null;
        }
    }

    private void connectionLoop() {
        long backoff = 1000;
        while (running.get()) {
            try {
                connect();
                backoff = 1000;
                readLoop();
            } catch (Exception e) {
                if (!running.get()) return;
                Tesseract.LOGGER.warn("Tesseract-service connection lost: {}", e.getMessage());
            } finally {
                boolean wasConnected = connected.getAndSet(false);
                closeSocket();
                if (wasConnected && handler != null) handler.onDisconnected();
            }
            if (!running.get()) return;
            Tesseract.LOGGER.info("Reconnecting to tesseract-service in {}ms", backoff);
            try {
                Thread.sleep(backoff);
            } catch (InterruptedException e) {
                return;
            }
            backoff = Math.min(backoff * 2, 30_000);
        }
    }

    private void connect() throws IOException {
        Tesseract.LOGGER.info("Connecting to tesseract-service at {}:{}", host, port);
        socket = new Socket(host, port);
        socket.setTcpNoDelay(true);
        socket.setKeepAlive(true);
        socket.setSoTimeout(60_000);
        dataIn = new DataInputStream(new BufferedInputStream(socket.getInputStream()));
        dataOut = new DataOutputStream(new BufferedOutputStream(socket.getOutputStream()));

        writeFrameDirect(TYPE_HELLO, makeHello(serverName));

        int length = dataIn.readInt();
        if (length < 1 || length > MAX_PACKET_SIZE) throw new IOException("Invalid frame length: " + length);
        byte[] frame = new byte[length];
        dataIn.readFully(frame);
        if (frame[0] != TYPE_HELLO_ACK) throw new IOException("Expected HELLO_ACK, got 0x" + Integer.toHexString(frame[0]));
        if (length > 1 && frame[1] != 0x00) throw new IOException("HELLO rejected with status " + frame[1]);

        connected.set(true);
        writeQueue.clear();

        Thread writer = new Thread(this::writeLoop, "tesseract-writer");
        writer.setDaemon(true);
        writer.start();

        Tesseract.LOGGER.info("Connected to tesseract-service");
        if (handler != null) handler.onConnected();
    }

    private void readLoop() throws IOException {
        while (running.get() && connected.get()) {
            int length = dataIn.readInt();
            if (length < 1 || length > MAX_PACKET_SIZE) throw new IOException("Invalid frame length: " + length);
            byte[] frame = new byte[length];
            dataIn.readFully(frame);

            byte type = frame[0];
            byte[] payload = new byte[length - 1];
            System.arraycopy(frame, 1, payload, 0, payload.length);
            dispatchPacket(type, payload);
        }
    }

    private void dispatchPacket(byte type, byte[] payload) {
        switch (type) {
            case TYPE_INV_UPDATE -> {
                if (payload.length < 24 || handler == null) return;
                UUID uuid = readUUID(payload, 0);
                long ts = ByteBuffer.wrap(payload, 16, 8).getLong();
                byte[] nbt = new byte[payload.length - 24];
                System.arraycopy(payload, 24, nbt, 0, nbt.length);
                handler.onInvUpdate(uuid, ts, nbt);
            }
            case TYPE_INV_RESPONSE -> {
                if (payload.length < 17 || handler == null) return;
                UUID uuid = readUUID(payload, 0);
                boolean found = payload[16] != 0;
                if (found && payload.length >= 25) {
                    long ts = ByteBuffer.wrap(payload, 17, 8).getLong();
                    byte[] nbt = new byte[payload.length - 25];
                    System.arraycopy(payload, 25, nbt, 0, nbt.length);
                    handler.onInvResponse(uuid, true, ts, nbt);
                } else {
                    handler.onInvResponse(uuid, false, 0, null);
                }
            }
            case TYPE_INV_PUSH_REJECT -> {
                if (payload.length < 24 || handler == null) return;
                UUID uuid = readUUID(payload, 0);
                long ts = ByteBuffer.wrap(payload, 16, 8).getLong();
                byte[] nbt = new byte[payload.length - 24];
                System.arraycopy(payload, 24, nbt, 0, nbt.length);
                handler.onInvPushReject(uuid, ts, nbt);
            }
            case TYPE_INV_PUSH_ACK -> {
                if (payload.length < 24 || handler == null) return;
                UUID uuid = readUUID(payload, 0);
                long ts = ByteBuffer.wrap(payload, 16, 8).getLong();
                handler.onInvPushAck(uuid, ts);
            }
            case TYPE_PING -> {
                if (payload.length >= 8) {
                    byte[] pong = new byte[8];
                    System.arraycopy(payload, 0, pong, 0, 8);
                    queueFrame(TYPE_PONG, pong);
                }
            }
            case TYPE_BATCH_RESULT -> {
                if (payload.length < 26) return;
                UUID uuid = readUUID(payload, 0);
                long ts = ByteBuffer.wrap(payload, 16, 8).getLong();
                int resultCount = Short.toUnsignedInt(ByteBuffer.wrap(payload, 24, 2).getShort());
                int offset = 26;
                byte[] statuses = new byte[resultCount];
                for (int i = 0; i < resultCount && offset < payload.length; i++) {
                    statuses[i] = payload[offset++];
                }
                byte[] snapshot = null;
                if (offset + 4 <= payload.length) {
                    int snapLen = ByteBuffer.wrap(payload, offset, 4).getInt();
                    offset += 4;
                    if (offset + snapLen <= payload.length) {
                        snapshot = new byte[snapLen];
                        System.arraycopy(payload, offset, snapshot, 0, snapLen);
                    }
                }
                BatchResult result = new BatchResult(uuid, ts, statuses, snapshot);
                var future = pendingResults.remove(uuid);
                if (future != null) {
                    future.complete(result);
                } else if (handler != null) {
                    handler.onBatchResult(result);
                }
            }
            case TYPE_PONG -> {}
            default -> Tesseract.LOGGER.debug("Unknown packet type from tesseract-service: 0x{}", Integer.toHexString(type));
        }
    }

    private void writeLoop() {
        try {
            while (running.get() && connected.get()) {
                byte[] frame = writeQueue.take();
                synchronized (dataOut) {
                    dataOut.write(frame);
                    while (!writeQueue.isEmpty()) {
                        byte[] next = writeQueue.poll();
                        if (next != null) dataOut.write(next);
                    }
                    dataOut.flush();
                }
            }
        } catch (InterruptedException ignored) {
        } catch (Exception e) {
            if (running.get() && connected.get()) {
                Tesseract.LOGGER.warn("Tesseract-service write error: {}", e.getMessage());
                closeSocket();
            }
        }
    }

    private void queueFrame(byte type, byte[] payload) {
        if (!connected.get()) return;
        byte[] frame = encodeFrame(type, payload);
        if (!writeQueue.offer(frame)) {
            Tesseract.LOGGER.warn("Tesseract write queue full, dropping packet 0x{}", Integer.toHexString(type));
        }
    }

    private void writeFrameDirect(byte type, byte[] payload) throws IOException {
        dataOut.write(encodeFrame(type, payload));
        dataOut.flush();
    }

    private static byte[] encodeFrame(byte type, byte[] payload) {
        int length = 1 + payload.length;
        ByteBuffer buf = ByteBuffer.allocate(4 + length);
        buf.putInt(length);
        buf.put(type);
        buf.put(payload);
        return buf.array();
    }

    private static byte[] uuidBytes(UUID uuid) {
        ByteBuffer buf = ByteBuffer.allocate(16);
        buf.putLong(uuid.getMostSignificantBits());
        buf.putLong(uuid.getLeastSignificantBits());
        return buf.array();
    }

    private static UUID readUUID(byte[] data, int offset) {
        long msb = ByteBuffer.wrap(data, offset, 8).getLong();
        long lsb = ByteBuffer.wrap(data, offset + 8, 8).getLong();
        return new UUID(msb, lsb);
    }

    private static byte[] makeHello(String name) {
        byte[] nameBytes = name.getBytes(StandardCharsets.UTF_8);
        ByteBuffer buf = ByteBuffer.allocate(2 + nameBytes.length + 1);
        buf.putShort((short) nameBytes.length);
        buf.put(nameBytes);
        buf.put(PROTOCOL_V2);
        return buf.array();
    }

    private void closeSocket() {
        connected.set(false);
        try {
            if (socket != null) socket.close();
        } catch (IOException ignored) {}
    }
}
