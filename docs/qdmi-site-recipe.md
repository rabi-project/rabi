# Integrating a real QDMI device (site recipe)

CI certifies the QDMI adapter against a compiled mock device — the ctypes
ABI path is real, the device is synthetic. Connecting a real QDMI site
cannot be CI'd here; execute this recipe at the partner site.

1. **Locate the device library.** Your QDMI vendor/site stack provides a
   shared library implementing the QDMI device interface.
2. **Check the ABI.** `rabi-adapter-qdmi --device libX.so` fails fast,
   listing any of the expected symbols (`rabi_qdmi.device.SYMBOLS`) the
   library lacks. QDMI implementations differ across versions; the
   binding's symbol table and argument shapes are centralized in
   `adapters/qdmi/src/rabi_qdmi/device.py` — adjust that one file to the
   site's QDMI version, nothing else.
3. **Set device facts.** `technology` (spec registry value) and the
   two-qubit native op default to superconducting/`cz`; correct them for
   the device in `describe()`.
4. **Serve + certify.**
   ```sh
   rabi-adapter-qdmi --device /opt/qdmi/libdevice.so --listen :50054
   rabi-conformance run --target localhost:50054 \
       --note "site: <name>, device: <device>, QDMI <version>"
   ```
   Send the signed report with the note filled in; certification is per
   declared capability, same as every other adapter.
5. **Register with rabi.** Add `qdmi=<host>:50054` to `RABI_ADAPTERS`.
