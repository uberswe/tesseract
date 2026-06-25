package com.gearworks.tesseract;

import net.minecraft.core.registries.Registries;
import net.minecraft.network.chat.Component;
import net.minecraft.world.item.BlockItem;
import net.minecraft.world.item.CreativeModeTab;
import net.minecraft.world.level.block.entity.BlockEntityType;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.neoforge.registries.DeferredBlock;
import net.neoforged.neoforge.registries.DeferredHolder;
import net.neoforged.neoforge.registries.DeferredItem;
import net.neoforged.neoforge.registries.DeferredRegister;

import java.util.function.Supplier;

/** Registers the Tesseract block, item, block entity and creative tab. */
public class TesseractRegistration {
    public static final DeferredRegister.Blocks BLOCKS = DeferredRegister.createBlocks(Tesseract.MOD_ID);
    public static final DeferredRegister.Items ITEMS = DeferredRegister.createItems(Tesseract.MOD_ID);
    public static final DeferredRegister<BlockEntityType<?>> BLOCK_ENTITIES =
            DeferredRegister.create(Registries.BLOCK_ENTITY_TYPE, Tesseract.MOD_ID);
    public static final DeferredRegister<CreativeModeTab> CREATIVE_TABS =
            DeferredRegister.create(Registries.CREATIVE_MODE_TAB, Tesseract.MOD_ID);

    public static final DeferredBlock<TesseractBlock> TESSERACT_BLOCK =
            BLOCKS.register("tesseract", TesseractBlock::new);

    public static final DeferredItem<BlockItem> TESSERACT_ITEM =
            ITEMS.registerSimpleBlockItem("tesseract", TESSERACT_BLOCK);

    public static final Supplier<BlockEntityType<TesseractBlockEntity>> TESSERACT_BE =
            BLOCK_ENTITIES.register("tesseract", () ->
                    BlockEntityType.Builder.of(TesseractBlockEntity::new, TESSERACT_BLOCK.get()).build(null));

    public static final DeferredHolder<CreativeModeTab, CreativeModeTab> TAB =
            CREATIVE_TABS.register("tesseract", () -> CreativeModeTab.builder()
                    .title(Component.translatable("itemGroup.tesseract.tesseract"))
                    .icon(() -> TESSERACT_ITEM.get().getDefaultInstance())
                    .displayItems((params, output) -> output.accept(TESSERACT_ITEM.get()))
                    .build());

    public static void register(IEventBus modEventBus) {
        BLOCKS.register(modEventBus);
        ITEMS.register(modEventBus);
        BLOCK_ENTITIES.register(modEventBus);
        CREATIVE_TABS.register(modEventBus);
    }
}
