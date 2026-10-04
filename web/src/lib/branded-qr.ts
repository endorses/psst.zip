import QRCode from "qrcode";

/** High correction, four-module quiet zone, and a small central brand backplate. */
export async function brandedQr(payload: string): Promise<string> {
  const canvas = document.createElement("canvas");
  const code = QRCode.create(payload, { errorCorrectionLevel: "H" });
  const scale = 8,
    quiet = 4;
  await QRCode.toCanvas(canvas, payload, {
    errorCorrectionLevel: "H",
    scale,
    margin: quiet,
    color: { dark: "#172B2A", light: "#FFFFFF" },
  });
  try {
    const logo = new Image();
    logo.src = "/brand/symbol.svg";
    await logo.decode();
    const context = canvas.getContext("2d")!;
    // Includes white separation; never cover more than 15% of active QR width.
    const size = Math.floor(code.modules.size * scale * 0.15),
      x = Math.floor((canvas.width - size) / 2);
    context.fillStyle = "#FFFFFF";
    context.fillRect(x, x, size, size);
    const inset = Math.max(2, Math.round(size * 0.1));
    context.drawImage(logo, x + inset, x + inset, size - inset * 2, size - inset * 2);
    return canvas.toDataURL("image/png");
  } catch {
    // A temporarily unavailable logo must not make the encrypted link unusable.
    return canvas.toDataURL("image/png");
  } finally {
    canvas.width = 0;
    canvas.height = 0;
  }
}
