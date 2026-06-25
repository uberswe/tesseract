package com.gearworks.tesseract;

import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.NbtIo;
import net.minecraft.nbt.Tag;
import net.minecraft.server.MinecraftServer;
import net.minecraft.world.item.ItemStack;
import net.neoforged.neoforge.items.ItemStackHandler;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArraySet;

/**
 * Bridges the in-world Tesseract blocks and the external sync service.
 *
 * <p>Each player ("owner") has one logical shared inventory. Every Tesseract block they own
 * shares a single {@link ItemStackHandler} (see {@link #createSharedHandler}) whose
 * insert/extract is forwarded to the service as an atomic batch op; the service validates it
 * against the authoritative inventory and returns the result (+ an optional fresh snapshot).
 * A local {@code authoritativeCache} mirrors the last snapshot so reads are instant.
 *
 * <p>The service — not the world — is the source of truth, so there is no database here: on
 * (re)connect we re-subscribe and re-request every active owner's inventory.
 */
public class TesseractSyncManager {
    private static final long SYNC_TIMEOUT_MS = 500;

    private static final Map<UUID, Set<TesseractBlockEntity>> activeByOwner = new ConcurrentHashMap<>();
    private static final Map<UUID, ItemStackHandler> sharedInventories = new ConcurrentHashMap<>();
    private static final Map<UUID, ItemStackHandler> authoritativeCache = new ConcurrentHashMap<>();
    private static final Map<UUID, Integer> authoritativeTotal = new ConcurrentHashMap<>();

    private static TesseractServiceClient client;
    private static volatile MinecraftServer server;

    public static void init(MinecraftServer mcServer) {
        server = mcServer;

        client = new TesseractServiceClient(
                TesseractConfig.getServiceHost(),
                TesseractConfig.getServicePort(),
                TesseractConfig.getServerName()
        );
        client.setHandler(new TesseractServiceClient.PacketHandler() {
            @Override
            public void onInvUpdate(UUID owner, long timestamp, byte[] nbtData) {
                handleRemoteUpdate(owner, timestamp, nbtData);
            }

            @Override
            public void onInvResponse(UUID owner, boolean found, long timestamp, byte[] nbtData) {
                if (found) {
                    handleRemoteUpdate(owner, timestamp, nbtData);
                }
            }

            @Override
            public void onInvPushReject(UUID owner, long timestamp, byte[] nbtData) {}

            @Override
            public void onInvPushAck(UUID owner, long timestamp) {}

            @Override
            public void onBatchResult(TesseractServiceClient.BatchResult result) {
                // Async batch results only carry remote updates — sync calls handle their own results.
                MinecraftServer srv = server;
                if (srv != null && result.snapshot() != null && result.snapshot().length > 0) {
                    srv.execute(() -> applySnapshot(result.owner(), result.snapshot(), result.timestamp()));
                }
            }

            @Override
            public void onConnected() {
                for (UUID owner : activeByOwner.keySet()) {
                    client.subscribe(owner);
                    client.requestInventory(owner);
                }
                Tesseract.LOGGER.info("Re-subscribed {} active tesseract owners after reconnect", activeByOwner.size());
            }

            @Override
            public void onDisconnected() {
                Tesseract.LOGGER.warn("Disconnected from tesseract-service");
            }
        });
        client.start();

        TesseractBlockEntity.syncCallback = new TesseractBlockEntity.SyncCallback() {
            @Override
            public void onOwnerSet(TesseractBlockEntity be) {
                loadInventory(be);
            }

            @Override
            public void onDirtyPush(TesseractBlockEntity be) {}

            @Override
            public void onRemoved(TesseractBlockEntity be) {
                unregister(be);
            }
        };

        Tesseract.LOGGER.info("TesseractSyncManager initialized (sync model, {}ms timeout), server: {}",
                SYNC_TIMEOUT_MS, TesseractConfig.getServerName());
    }

    public static void shutdown() {
        TesseractBlockEntity.syncCallback = null;
        if (client != null) client.stop();
        activeByOwner.clear();
        sharedInventories.clear();
        authoritativeCache.clear();
        authoritativeTotal.clear();
        server = null;
        Tesseract.LOGGER.info("TesseractSyncManager shut down.");
    }

    public static void register(TesseractBlockEntity be) {
        if (be.getOwnerUuid() == null) return;
        UUID owner = be.getOwnerUuid();
        activeByOwner.computeIfAbsent(owner, k -> new CopyOnWriteArraySet<>()).add(be);
        ItemStackHandler shared = sharedInventories.computeIfAbsent(owner, TesseractSyncManager::createSharedHandler);
        be.setSharedInventory(shared);
    }

    public static void unregister(TesseractBlockEntity be) {
        if (be.getOwnerUuid() == null) return;
        UUID owner = be.getOwnerUuid();
        be.setSharedInventory(null);
        Set<TesseractBlockEntity> set = activeByOwner.get(owner);
        if (set != null) {
            set.remove(be);
            if (set.isEmpty()) {
                activeByOwner.remove(owner);
                sharedInventories.remove(owner);
                authoritativeCache.remove(owner);
                authoritativeTotal.remove(owner);
                if (client != null) client.unsubscribe(owner);
            }
        }
    }

    private static ItemStackHandler createSharedHandler(UUID owner) {
        return new ItemStackHandler(TesseractBlockEntity.SLOTS) {
            @Override
            public ItemStack extractItem(int slot, int amount, boolean simulate) {
                // Check cache to see if items exist.
                ItemStackHandler cache = authoritativeCache.get(owner);
                if (cache == null) return ItemStack.EMPTY;
                ItemStack available = cache.getStackInSlot(slot);
                if (available.isEmpty()) return ItemStack.EMPTY;

                // Never hand out a stack larger than the item's max stack size
                // (max-stack-1 items must come out one per extract). This also
                // safely drains any legacy slot that was overstacked before this fix.
                int maxStack = available.getMaxStackSize();

                if (simulate) {
                    int toExtract = Math.min(Math.min(amount, available.getCount()), maxStack);
                    ItemStack result = available.copy();
                    result.setCount(toExtract);
                    return result;
                }

                // Sync extract from service.
                int extractAmount = Math.min(Math.min(amount, available.getCount()), maxStack);
                var ops = List.of(new TesseractServiceClient.BatchOperation(
                        TesseractServiceClient.OP_EXTRACT, slot, extractAmount, null));

                TesseractServiceClient.BatchResult result = client != null
                        ? client.sendBatchOpsSync(owner, ops, SYNC_TIMEOUT_MS) : null;

                boolean rejected = result != null && result.statuses() != null
                        && result.statuses().length > 0
                        && result.statuses()[0] != TesseractServiceClient.RESULT_ACCEPTED;
                if (rejected) {
                    return ItemStack.EMPTY;
                }

                // On timeout (result==null) extract optimistically to prevent item loss.
                // Snapshot reconciliation will correct any discrepancy.
                ItemStack extracted = available.copy();
                extracted.setCount(extractAmount);
                int remaining = available.getCount() - extractAmount;
                if (remaining <= 0) {
                    cache.setStackInSlot(slot, ItemStack.EMPTY);
                } else {
                    ItemStack left = available.copy();
                    left.setCount(remaining);
                    cache.setStackInSlot(slot, left);
                }
                authoritativeTotal.merge(owner, -extractAmount, Integer::sum);

                if (result != null && result.snapshot() != null && result.snapshot().length > 0) {
                    applySnapshot(owner, result.snapshot(), result.timestamp());
                }

                return extracted;
            }

            @Override
            public ItemStack insertItem(int slot, ItemStack stack, boolean simulate) {
                if (stack.isEmpty()) return ItemStack.EMPTY;

                // Respect item identity and per-item max stack size. Without these
                // checks a different item could be merged into an occupied slot
                // (destroying it), and max-stack-1 items (enchanted books, tools,
                // potions) could be overstacked into an invalid count > 1.
                ItemStackHandler slotCache = authoritativeCache.get(owner);
                ItemStack slotExisting = slotCache != null ? slotCache.getStackInSlot(slot) : ItemStack.EMPTY;
                if (!slotExisting.isEmpty() && !ItemStack.isSameItemSameComponents(slotExisting, stack)) {
                    return stack; // incompatible item already occupies this slot — reject
                }

                int slotLimit = Math.min(getSlotLimit(slot), stack.getMaxStackSize());
                int slotSpace = slotLimit - slotExisting.getCount();
                if (slotSpace <= 0) return stack; // slot full for this item (e.g. 1 enchanted book)

                int currentTotal = authoritativeTotal.getOrDefault(owner, 0);
                int available = TesseractBlockEntity.MAX_TOTAL_ITEMS - currentTotal;
                if (available <= 0) return stack;

                int accepted = Math.min(stack.getCount(), Math.min(slotSpace, available));
                if (accepted <= 0) return stack;

                if (simulate) {
                    if (accepted >= stack.getCount()) return ItemStack.EMPTY;
                    ItemStack remainder = stack.copy();
                    remainder.setCount(stack.getCount() - accepted);
                    return remainder;
                }

                MinecraftServer srv = server;
                if (srv == null) return stack;
                var lookup = srv.registryAccess();

                ItemStack toInsert = stack.copy();
                toInsert.setCount(accepted);

                CompoundTag itemTag = (CompoundTag) toInsert.save(lookup);
                itemTag.remove("count");
                byte[] nbtBytes = serializeCompoundTagInner(itemTag);

                var ops = List.of(new TesseractServiceClient.BatchOperation(
                        TesseractServiceClient.OP_INSERT, slot, accepted, nbtBytes));

                TesseractServiceClient.BatchResult result = client != null
                        ? client.sendBatchOpsSync(owner, ops, SYNC_TIMEOUT_MS) : null;

                boolean rejected = result != null && result.statuses() != null
                        && result.statuses().length > 0
                        && result.statuses()[0] != TesseractServiceClient.RESULT_ACCEPTED;
                if (rejected) {
                    return stack;
                }

                // On timeout (result==null) consume optimistically to prevent retry dupes.
                // Snapshot reconciliation will correct any discrepancy.

                // Update cache.
                ItemStackHandler cache = authoritativeCache.get(owner);
                if (cache != null) {
                    ItemStack existing = cache.getStackInSlot(slot);
                    if (existing.isEmpty()) {
                        cache.setStackInSlot(slot, toInsert.copy());
                    } else {
                        ItemStack combined = existing.copy();
                        combined.setCount(existing.getCount() + accepted);
                        cache.setStackInSlot(slot, combined);
                    }
                }
                authoritativeTotal.merge(owner, accepted, Integer::sum);

                if (result != null && result.snapshot() != null && result.snapshot().length > 0) {
                    applySnapshot(owner, result.snapshot(), result.timestamp());
                }

                if (accepted >= stack.getCount()) return ItemStack.EMPTY;
                ItemStack remainder = stack.copy();
                remainder.setCount(stack.getCount() - accepted);
                return remainder;
            }

            @Override
            public int getSlots() {
                return TesseractBlockEntity.SLOTS;
            }

            @Override
            public int getSlotLimit(int slot) {
                return 64;
            }

            @Override
            public ItemStack getStackInSlot(int slot) {
                ItemStackHandler cache = authoritativeCache.get(owner);
                if (cache != null) {
                    return cache.getStackInSlot(slot);
                }
                return super.getStackInSlot(slot);
            }

            @Override
            public void setStackInSlot(int slot, ItemStack stack) {
                super.setStackInSlot(slot, stack);
            }

            @Override
            protected void onContentsChanged(int slot) {
                Set<TesseractBlockEntity> blocks = activeByOwner.get(owner);
                if (blocks != null) {
                    for (TesseractBlockEntity be : blocks) {
                        be.setChanged();
                    }
                }
            }
        };
    }

    private static void applySnapshot(UUID owner, byte[] snapshotData, long timestamp) {
        try {
            CompoundTag tag = decompress(snapshotData);
            ListTag items = tag.getList("Items", Tag.TAG_COMPOUND);
            MinecraftServer srv = server;
            if (srv == null) return;
            var lookup = srv.registryAccess();

            ItemStackHandler cache = authoritativeCache.computeIfAbsent(owner,
                    k -> new ItemStackHandler(TesseractBlockEntity.SLOTS));

            int total = 0;
            for (int i = 0; i < TesseractBlockEntity.SLOTS; i++) {
                ItemStack newStack = ItemStack.EMPTY;
                if (i < items.size()) {
                    CompoundTag compound = items.getCompound(i);
                    newStack = ItemStack.parseOptional(lookup, compound);
                    if (!newStack.isEmpty() && compound.contains("count")) {
                        int rawCount = compound.getInt("count");
                        if (rawCount > 0) {
                            newStack.setCount(rawCount);
                        }
                    }
                }
                cache.setStackInSlot(i, newStack);
                total += newStack.getCount();
            }
            authoritativeTotal.put(owner, total);

            Set<TesseractBlockEntity> blocks = activeByOwner.get(owner);
            if (blocks != null) {
                for (TesseractBlockEntity b : blocks) {
                    b.setLastSyncTimestamp(timestamp);
                }
            }
        } catch (Exception e) {
            Tesseract.LOGGER.warn("Failed to apply snapshot for {}: {}", owner, e.getMessage());
        }
    }

    public static void loadInventory(TesseractBlockEntity be) {
        if (be.getOwnerUuid() == null) return;
        UUID owner = be.getOwnerUuid();
        boolean alreadyLoaded = sharedInventories.containsKey(owner);
        register(be);
        if (alreadyLoaded) return;

        // Pull the authoritative inventory from the service. If we are not connected yet, the
        // onConnected handler re-subscribes and re-requests every active owner once the socket
        // comes back, so there is nothing to do here.
        if (client != null && client.isConnected()) {
            client.subscribe(owner);
            client.requestInventory(owner);
        }
    }

    private static void handleRemoteUpdate(UUID owner, long timestamp, byte[] nbtData) {
        Set<TesseractBlockEntity> blocks = activeByOwner.get(owner);
        if (blocks == null || blocks.isEmpty()) return;
        MinecraftServer srv = server;
        if (srv != null) {
            srv.execute(() -> applySnapshot(owner, nbtData, timestamp));
        }
    }

    /**
     * Serializes a CompoundTag to the service's "inner" NBT form: a raw payload without the
     * 3-byte {@code TAG_Compound + empty-name} header that {@link NbtIo#write} prepends.
     */
    static byte[] serializeCompoundTagInner(CompoundTag tag) {
        try {
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            DataOutputStream dos = new DataOutputStream(baos);
            NbtIo.write(tag, dos);
            byte[] full = baos.toByteArray();
            if (full.length > 4) {
                byte[] inner = new byte[full.length - 4];
                System.arraycopy(full, 3, inner, 0, inner.length);
                return inner;
            }
            return new byte[0];
        } catch (Exception e) {
            Tesseract.LOGGER.error("Failed to serialize CompoundTag: {}", e.getMessage());
            return new byte[0];
        }
    }

    private static CompoundTag decompress(byte[] data) throws Exception {
        return NbtIo.readCompressed(new DataInputStream(new ByteArrayInputStream(data)),
                net.minecraft.nbt.NbtAccounter.unlimitedHeap());
    }
}
