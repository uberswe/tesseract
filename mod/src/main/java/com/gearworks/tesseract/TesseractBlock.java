package com.gearworks.tesseract;

import net.minecraft.core.BlockPos;
import net.minecraft.world.entity.LivingEntity;
import net.minecraft.world.item.ItemStack;
import net.minecraft.world.level.Level;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.EntityBlock;
import net.minecraft.world.level.block.SoundType;
import net.minecraft.world.level.block.entity.BlockEntity;
import net.minecraft.world.level.block.entity.BlockEntityTicker;
import net.minecraft.world.level.block.entity.BlockEntityType;
import net.minecraft.world.level.block.state.BlockBehaviour;
import net.minecraft.world.level.block.state.BlockState;
import net.minecraft.world.level.material.MapColor;
import org.jetbrains.annotations.Nullable;

public class TesseractBlock extends Block implements EntityBlock {

    public TesseractBlock() {
        super(BlockBehaviour.Properties.of()
                .mapColor(MapColor.COLOR_PURPLE)
                .strength(50.0F, 1200.0F)
                .sound(SoundType.NETHERITE_BLOCK)
                .lightLevel(state -> 7)
                .requiresCorrectToolForDrops());
    }

    @Nullable
    @Override
    public BlockEntity newBlockEntity(BlockPos pos, BlockState state) {
        return new TesseractBlockEntity(pos, state);
    }

    @Nullable
    @Override
    public <T extends BlockEntity> BlockEntityTicker<T> getTicker(Level level, BlockState state, BlockEntityType<T> type) {
        if (level.isClientSide()) return null;
        return type == TesseractRegistration.TESSERACT_BE.get()
                ? (lvl, pos, st, be) -> ((TesseractBlockEntity) be).serverTick()
                : null;
    }

    @Override
    public void setPlacedBy(Level level, BlockPos pos, BlockState state, @Nullable LivingEntity placer, ItemStack stack) {
        super.setPlacedBy(level, pos, state, placer, stack);
        if (!level.isClientSide() && placer != null) {
            BlockEntity be = level.getBlockEntity(pos);
            if (be instanceof TesseractBlockEntity tesseract) {
                tesseract.setOwner(placer.getUUID());
            }
        }
    }

    @Override
    public BlockState playerWillDestroy(Level level, BlockPos pos, BlockState state, net.minecraft.world.entity.player.Player player) {
        if (!level.isClientSide()) {
            BlockEntity be = level.getBlockEntity(pos);
            if (be instanceof TesseractBlockEntity tesseract) {
                tesseract.onRemoved();
            }
        }
        return super.playerWillDestroy(level, pos, state, player);
    }
}
