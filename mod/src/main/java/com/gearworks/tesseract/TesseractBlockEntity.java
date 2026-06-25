package com.gearworks.tesseract;

import net.minecraft.core.BlockPos;
import net.minecraft.core.HolderLookup;
import net.minecraft.nbt.CompoundTag;
import net.minecraft.nbt.ListTag;
import net.minecraft.nbt.Tag;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.state.BlockState;
import net.neoforged.neoforge.items.ItemStackHandler;

import java.util.UUID;

public class TesseractBlockEntity extends BlockEntity {
    public static final int SLOTS = 160;
    public static final int MAX_TOTAL_ITEMS = 10240;
    public interface SyncCallback {
        void onOwnerSet(TesseractBlockEntity be);
        void onDirtyPush(TesseractBlockEntity be);
        void onRemoved(TesseractBlockEntity be);
    }

    public static SyncCallback syncCallback;

    private UUID ownerUuid;
    private final ItemStackHandler localInventory = new ItemStackHandler(SLOTS);
    private ItemStackHandler sharedInventory;
    private boolean dirty = false;
    private long lastSyncTimestamp = 0;

    public TesseractBlockEntity(BlockPos pos, BlockState state) {
        super(TesseractRegistration.TESSERACT_BE.get(), pos, state);
    }

    public void setSharedInventory(ItemStackHandler shared) {
        this.sharedInventory = shared;
    }

    public ItemStackHandler getInventory() {
        return sharedInventory != null ? sharedInventory : localInventory;
    }

    public int getTotalItemCount() {
        ItemStackHandler inv = getInventory();
        int total = 0;
        for (int i = 0; i < SLOTS; i++) {
            total += inv.getStackInSlot(i).getCount();
        }
        return total;
    }

    public void setOwner(UUID uuid) {
        this.ownerUuid = uuid;
        setChanged();
        if (syncCallback != null) syncCallback.onOwnerSet(this);
    }

    public UUID getOwnerUuid() {
        return ownerUuid;
    }

    public long getLastSyncTimestamp() {
        return lastSyncTimestamp;
    }

    public void setLastSyncTimestamp(long ts) {
        this.lastSyncTimestamp = ts;
    }

    public boolean isDirty() {
        return dirty;
    }

    public void markDirty() {
        dirty = true;
        setChanged();
    }

    public void clearDirty() {
        dirty = false;
    }

    public void serverTick() {
        if (ownerUuid == null) return;
        if (sharedInventory == null && syncCallback != null) {
            syncCallback.onOwnerSet(this);
        }
    }

    public void onRemoved() {
        if (syncCallback != null) syncCallback.onRemoved(this);
    }

    public void applyRemoteInventory(ListTag items, long timestamp) {
        if (timestamp <= lastSyncTimestamp) return;
        lastSyncTimestamp = timestamp;
        var lookup = level != null ? level.registryAccess() : null;
        if (lookup == null) return;
        ItemStackHandler inv = getInventory();
        for (int i = 0; i < SLOTS; i++) {
            if (i < items.size()) {
                CompoundTag itemTag = items.getCompound(i);
                inv.setStackInSlot(i, ItemStack.parseOptional(lookup, itemTag));
            } else {
                inv.setStackInSlot(i, ItemStack.EMPTY);
            }
        }
        dirty = false;
    }

    public ListTag serializeInventory() {
        ListTag items = new ListTag();
        var lookup = level != null ? level.registryAccess() : null;
        if (lookup == null) return items;
        ItemStackHandler inv = getInventory();
        for (int i = 0; i < SLOTS; i++) {
            ItemStack stack = inv.getStackInSlot(i);
            if (stack.isEmpty()) {
                items.add(new CompoundTag());
            } else {
                items.add((Tag) stack.save(lookup));
            }
        }
        return items;
    }

    @Override
    protected void saveAdditional(CompoundTag tag, HolderLookup.Provider registries) {
        super.saveAdditional(tag, registries);
        if (ownerUuid != null) {
            tag.putUUID("Owner", ownerUuid);
        }
        tag.put("Items", serializeInventory());
        tag.putLong("SyncTimestamp", lastSyncTimestamp);
    }

    @Override
    protected void loadAdditional(CompoundTag tag, HolderLookup.Provider registries) {
        super.loadAdditional(tag, registries);
        if (tag.hasUUID("Owner")) {
            ownerUuid = tag.getUUID("Owner");
        }
        if (tag.contains("Items", Tag.TAG_LIST)) {
            ListTag items = tag.getList("Items", Tag.TAG_COMPOUND);
            for (int i = 0; i < SLOTS && i < items.size(); i++) {
                CompoundTag itemTag = items.getCompound(i);
                localInventory.setStackInSlot(i, ItemStack.parseOptional(registries, itemTag));
            }
        }
        lastSyncTimestamp = tag.getLong("SyncTimestamp");
    }
}
