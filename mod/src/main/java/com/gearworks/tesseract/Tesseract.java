package com.gearworks.tesseract;

import com.mojang.logging.LogUtils;
import net.neoforged.bus.api.IEventBus;
import net.neoforged.fml.ModContainer;
import net.neoforged.fml.common.Mod;
import net.neoforged.fml.config.ModConfig;
import net.neoforged.neoforge.capabilities.Capabilities;
import net.neoforged.neoforge.capabilities.RegisterCapabilitiesEvent;
import net.neoforged.neoforge.common.NeoForge;
import net.neoforged.neoforge.event.server.ServerStartedEvent;
import net.neoforged.neoforge.event.server.ServerStoppingEvent;
import org.slf4j.Logger;

/**
 * Entry point for the standalone Tesseract mod.
 *
 * <p>The Tesseract is a wireless, cross-server storage block. Its inventory is not stored
 * in the world — it lives in an external {@code tesseract-service} that every connected
 * server talks to over a small binary TCP protocol. Items inserted into a player's Tesseract
 * on one server become available from any of their Tesseracts on any other connected server.
 *
 * <p>The block exposes only the NeoForge {@code ItemHandler} capability, so it is driven by
 * automation (hoppers, Create, pipes…) rather than a GUI. Every insert/extract is mediated by
 * {@link TesseractSyncManager}, which routes the operation through the service so the service
 * stays the single source of truth.
 */
@Mod(Tesseract.MOD_ID)
public class Tesseract {
    public static final String MOD_ID = "tesseract";
    public static final Logger LOGGER = LogUtils.getLogger();

    public Tesseract(IEventBus modEventBus, ModContainer modContainer) {
        modContainer.registerConfig(ModConfig.Type.COMMON, TesseractConfig.SPEC);

        TesseractRegistration.register(modEventBus);
        modEventBus.addListener(this::registerCapabilities);

        // The sync manager opens/closes its connection to the service with the server lifecycle.
        NeoForge.EVENT_BUS.addListener((ServerStartedEvent e) -> TesseractSyncManager.init(e.getServer()));
        NeoForge.EVENT_BUS.addListener((ServerStoppingEvent e) -> TesseractSyncManager.shutdown());
    }

    private void registerCapabilities(RegisterCapabilitiesEvent event) {
        // Hoppers/pipes/Create interact with the block through this ItemHandler; the handler
        // returned by getInventory() is the sync-backed one once an owner is registered.
        event.registerBlockEntity(
                Capabilities.ItemHandler.BLOCK,
                TesseractRegistration.TESSERACT_BE.get(),
                (be, side) -> be.getInventory()
        );
    }
}
