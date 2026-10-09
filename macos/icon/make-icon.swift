// Renders the Replicant app icon: a bold, forward-leaning "R" with a hot
// chrome gradient on a dark rounded square, in the spirit of the Blade
// Runner title lettering. Pure CoreGraphics, no fonts, so it builds the
// same everywhere. Usage: swift make-icon.swift <out dir>
// Writes icon_<n>x<n>[@2x].png files for iconutil, plus preview.png.
// A second argument picks the design: "r" (default), "eye", or "figure".
import AppKit
import CoreGraphics
import Foundation

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."
let variant = CommandLine.arguments.count > 2 ? CommandLine.arguments[2] : "r"
try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

func rgba(_ hex: UInt32, _ a: CGFloat = 1) -> CGColor {
    CGColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255, blue: CGFloat(hex & 0xff) / 255, alpha: a)
}

/// The letter in a 0...100 box (y up), before the lean.
func letterPath() -> CGPath {
    let p = CGMutablePath()
    // Outer silhouette of stem + bowl, drawn as one shape with the counter
    // cut out (even-odd), then the leg as a separate polygon.
    let stemL: CGFloat = 16, stemR: CGFloat = 36
    let bowlR: CGFloat = 78, top: CGFloat = 94, bowlBottom: CGFloat = 50, bottom: CGFloat = 6
    let r: CGFloat = 15 // outer bowl radius
    // Stem + bowl outline.
    p.move(to: CGPoint(x: stemL, y: bottom))
    p.addLine(to: CGPoint(x: stemL, y: top))
    p.addLine(to: CGPoint(x: bowlR - r, y: top))
    p.addArc(tangent1End: CGPoint(x: bowlR, y: top), tangent2End: CGPoint(x: bowlR, y: top - r), radius: r)
    p.addLine(to: CGPoint(x: bowlR, y: bowlBottom + r))
    p.addArc(tangent1End: CGPoint(x: bowlR, y: bowlBottom), tangent2End: CGPoint(x: bowlR - r, y: bowlBottom), radius: r)
    p.addLine(to: CGPoint(x: stemR, y: bowlBottom))
    p.addLine(to: CGPoint(x: stemR, y: bottom))
    p.closeSubpath()
    // Counter.
    let cl: CGFloat = stemR, cr: CGFloat = bowlR - 17, ct: CGFloat = top - 17, cb: CGFloat = bowlBottom + 17
    let cradius: CGFloat = 4
    p.move(to: CGPoint(x: cl, y: cb))
    p.addLine(to: CGPoint(x: cl, y: ct))
    p.addLine(to: CGPoint(x: cr - cradius, y: ct))
    p.addArc(tangent1End: CGPoint(x: cr, y: ct), tangent2End: CGPoint(x: cr, y: ct - cradius), radius: cradius)
    p.addLine(to: CGPoint(x: cr, y: cb + cradius))
    p.addArc(tangent1End: CGPoint(x: cr, y: cb), tangent2End: CGPoint(x: cr - cradius, y: cb), radius: cradius)
    p.closeSubpath()
    // Leg: a straight diagonal from the bowl to the baseline, bottom right.
    let leg = CGMutablePath()
    leg.move(to: CGPoint(x: 44, y: bowlBottom + 1))
    leg.addLine(to: CGPoint(x: 66, y: bowlBottom + 1))
    leg.addLine(to: CGPoint(x: 90, y: bottom))
    leg.addLine(to: CGPoint(x: 67, y: bottom))
    leg.closeSubpath()
    p.addPath(leg)
    return p
}

/// Option two: an eye reflecting fire and neon, the film's opening image.
/// Everything is geometry: almond lids, a hot iris with a city skyline
/// reflected in it, a black pupil, and a glint.
func drawEye(_ ctx: CGContext, cs: CGColorSpace, tile: CGRect, s: CGFloat) {
    let cx = tile.midX, cy = tile.midY + tile.height * 0.02
    let w = tile.width * 0.76, h = tile.height * 0.42
    // Almond: two arcs meeting at the corners.
    let eye = CGMutablePath()
    eye.move(to: CGPoint(x: cx - w / 2, y: cy))
    eye.addQuadCurve(to: CGPoint(x: cx + w / 2, y: cy), control: CGPoint(x: cx, y: cy + h * 1.15))
    eye.addQuadCurve(to: CGPoint(x: cx - w / 2, y: cy), control: CGPoint(x: cx, y: cy - h * 1.15))
    eye.closeSubpath()

    // Warm glow bleeding past the lids.
    ctx.saveGState()
    ctx.setShadow(offset: .zero, blur: s * 0.09, color: rgba(0xFF4A1A, 0.6))
    ctx.addPath(eye)
    ctx.setFillColor(rgba(0x2A0A0A))
    ctx.fillPath()
    ctx.restoreGState()

    ctx.saveGState()
    ctx.addPath(eye)
    ctx.clip()
    // Sclera: dark, lit from the iris.
    let sclera = CGGradient(colorsSpace: cs, colors: [rgba(0x6B1A12), rgba(0x1A0708), rgba(0x080405)] as CFArray, locations: [0, 0.55, 1])!
    ctx.drawRadialGradient(sclera, startCenter: CGPoint(x: cx, y: cy), startRadius: 0, endCenter: CGPoint(x: cx, y: cy), endRadius: w * 0.55, options: [])

    // Iris.
    let ir = h * 0.78
    let irisRect = CGRect(x: cx - ir, y: cy - ir, width: 2 * ir, height: 2 * ir)
    ctx.saveGState()
    ctx.addEllipse(in: irisRect)
    ctx.clip()
    let iris = CGGradient(colorsSpace: cs,
        colors: [rgba(0xFFE2A8), rgba(0xFF9A2E), rgba(0xFF4F17), rgba(0xB8151C), rgba(0x5E0A10)] as CFArray,
        locations: [0, 0.2, 0.5, 0.8, 1])!
    ctx.drawRadialGradient(iris, startCenter: CGPoint(x: cx, y: cy), startRadius: ir * 0.3, endCenter: CGPoint(x: cx, y: cy), endRadius: ir, options: [])
    // Radial fibres.
    ctx.setStrokeColor(rgba(0x3A0608, 0.35))
    ctx.setLineWidth(max(1, s * 0.003))
    for i in 0..<72 {
        let a = CGFloat(i) / 72 * .pi * 2
        let r0 = ir * (0.42 + 0.08 * CGFloat((i * 7) % 5) / 5)
        ctx.move(to: CGPoint(x: cx + cos(a) * r0, y: cy + sin(a) * r0))
        ctx.addLine(to: CGPoint(x: cx + cos(a) * ir, y: cy + sin(a) * ir))
    }
    ctx.strokePath()
    // A reflected skyline: dark towers across the lower half of the iris,
    // with a few lit windows.
    ctx.setFillColor(rgba(0x1C0507, 0.85))
    let base = cy - ir * 0.62
    let towers: [(CGFloat, CGFloat, CGFloat)] = [(-0.78, 0.12, 0.22), (-0.64, 0.09, 0.34), (-0.53, 0.13, 0.26), (-0.38, 0.1, 0.42), (-0.26, 0.14, 0.3), (-0.1, 0.09, 0.5), (0.0, 0.12, 0.36), (0.14, 0.1, 0.44), (0.26, 0.15, 0.28), (0.43, 0.09, 0.38), (0.54, 0.12, 0.24), (0.68, 0.1, 0.3)]
    for (x, tw, th) in towers {
        ctx.fill(CGRect(x: cx + x * ir, y: cy - ir, width: tw * ir, height: base + th * ir - (cy - ir)))
    }
    ctx.setFillColor(rgba(0xFFD37A, 0.95))
    for (i, (x, tw, th)) in towers.enumerated() {
        let rows = Int(th * 10)
        for row in 0..<rows where (row + i) % 2 == 0 {
            ctx.fill(CGRect(x: cx + (x + tw * 0.25) * ir, y: base + CGFloat(row) * ir * 0.045 + ir * 0.02, width: tw * ir * 0.18, height: ir * 0.02))
            ctx.fill(CGRect(x: cx + (x + tw * 0.6) * ir, y: base + CGFloat(row) * ir * 0.045 + ir * 0.02, width: tw * ir * 0.18, height: ir * 0.02))
        }
    }
    // Pupil.
    let pr = ir * 0.36
    ctx.setFillColor(rgba(0x05020A))
    ctx.fillEllipse(in: CGRect(x: cx - pr, y: cy - pr, width: 2 * pr, height: 2 * pr))
    // Glint.
    ctx.setFillColor(rgba(0xFFFFFF, 0.9))
    ctx.fillEllipse(in: CGRect(x: cx - pr * 0.55, y: cy + pr * 0.25, width: pr * 0.5, height: pr * 0.5))
    ctx.restoreGState()

    // Limbal ring.
    ctx.addEllipse(in: irisRect)
    ctx.setStrokeColor(rgba(0x3A0608, 0.9))
    ctx.setLineWidth(max(1, s * 0.006))
    ctx.strokePath()
    ctx.restoreGState()

    // Lids: a dark rim with an orange edge light on the upper lid.
    ctx.addPath(eye)
    ctx.setStrokeColor(rgba(0x120406))
    ctx.setLineWidth(max(1, s * 0.012))
    ctx.strokePath()
    let upper = CGMutablePath()
    upper.move(to: CGPoint(x: cx - w / 2, y: cy))
    upper.addQuadCurve(to: CGPoint(x: cx + w / 2, y: cy), control: CGPoint(x: cx, y: cy + h * 1.15))
    ctx.addPath(upper)
    ctx.setStrokeColor(rgba(0xFF7A2A, 0.8))
    ctx.setLineWidth(max(1, s * 0.005))
    ctx.strokePath()
}

/// Option three: an original noir silhouette, head and shoulders in
/// profile with the collar up, rim-lit in orange against a city glow,
/// with rain. Nothing traced; the profile is a handful of curves.
func drawFigure(_ ctx: CGContext, cs: CGColorSpace, tile: CGRect, s: CGFloat) {
    // Warm burst low on the left, like a searchlight through smoke.
    let burst = CGGradient(colorsSpace: cs, colors: [rgba(0xFFB347, 0.85), rgba(0xE8521C, 0.55), rgba(0x5A0A10, 0.25), rgba(0x000000, 0)] as CFArray, locations: [0, 0.3, 0.6, 1])!
    ctx.drawRadialGradient(burst, startCenter: CGPoint(x: tile.minX + tile.width * 0.22, y: tile.minY + tile.height * 0.3), startRadius: 0,
                           endCenter: CGPoint(x: tile.minX + tile.width * 0.22, y: tile.minY + tile.height * 0.3), endRadius: tile.width * 0.75, options: [])
    // Distant towers in the glow.
    ctx.setFillColor(rgba(0x1A0609, 0.7))
    let towers: [(CGFloat, CGFloat, CGFloat)] = [(0.0, 0.06, 0.2), (0.07, 0.04, 0.3), (0.12, 0.07, 0.17), (0.2, 0.05, 0.34), (0.26, 0.04, 0.24), (0.31, 0.06, 0.19), (0.38, 0.04, 0.27)]
    for (x, w, h) in towers {
        ctx.fill(CGRect(x: tile.minX + x * tile.width, y: tile.minY, width: w * tile.width, height: h * tile.height))
    }
    // Rain: thin slanted streaks.
    ctx.saveGState()
    ctx.setStrokeColor(rgba(0xFFD6A0, 0.16))
    ctx.setLineWidth(max(0.5, s * 0.0025))
    var seed: UInt32 = 7
    func rnd() -> CGFloat { seed = seed &* 1664525 &+ 1013904223; return CGFloat(seed >> 8) / CGFloat(1 << 24) }
    for _ in 0..<80 {
        let x = tile.minX + rnd() * tile.width, y = tile.minY + rnd() * tile.height
        let len = tile.height * (0.04 + rnd() * 0.08)
        ctx.move(to: CGPoint(x: x, y: y))
        ctx.addLine(to: CGPoint(x: x - len * 0.18, y: y - len))
    }
    ctx.strokePath()
    ctx.restoreGState()

    // The figure is two shapes filled together: a head with neck, and a
    // body with a turned-up collar. Coordinates in a 0...100 box, y up,
    // facing left.
    func P(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
        CGPoint(x: tile.minX + tile.width * (0.12 + x * 0.0082), y: tile.minY + tile.height * (-0.02 + x * 0 + y * 0.0092))
    }
    let head = CGMutablePath()
    head.move(to: P(60, 98))                                              // crown
    head.addCurve(to: P(40, 86), control1: P(50, 99), control2: P(42, 94))   // hairline
    head.addCurve(to: P(37, 73), control1: P(37, 82), control2: P(36, 77))   // forehead
    head.addCurve(to: P(30, 62), control1: P(38, 70), control2: P(32, 66))   // brow and nose
    head.addCurve(to: P(36, 58), control1: P(29, 59), control2: P(33, 58))   // nostril
    head.addCurve(to: P(34, 50), control1: P(35, 55), control2: P(33, 52))   // lips
    head.addCurve(to: P(36, 43), control1: P(36, 48), control2: P(34, 44))   // chin
    head.addCurve(to: P(50, 37), control1: P(40, 40), control2: P(45, 37))   // jaw
    head.addLine(to: P(50, 26))                                           // neck front, into the collar
    head.addLine(to: P(66, 26))                                           // neck back
    head.addCurve(to: P(66, 60), control1: P(66, 34), control2: P(64, 50))   // nape
    head.addCurve(to: P(60, 98), control1: P(76, 70), control2: P(78, 96))   // back of the head
    head.closeSubpath()
    let body = CGMutablePath()
    body.move(to: P(-5, -6))
    body.addCurve(to: P(14, 24), control1: P(2, 8), control2: P(8, 20))      // far shoulder
    body.addCurve(to: P(58, 38), control1: P(26, 32), control2: P(44, 38))   // collar rising behind the chin
    body.addCurve(to: P(105, 8), control1: P(80, 38), control2: P(98, 22))   // near shoulder
    body.addLine(to: P(105, -6))
    body.closeSubpath()
    // The head and body wind in opposite directions, so they are filled
    // one at a time rather than as one nonzero path (which would punch a
    // hole where the neck overlaps the collar).
    func fill(_ color: CGColor) {
        for part in [head, body] {
            ctx.addPath(part)
            ctx.setFillColor(color)
            ctx.fillPath()
        }
    }
    // Rim light: an orange copy, then the black figure shifted right so a
    // sliver of orange survives along the lit edge.
    ctx.saveGState()
    fill(rgba(0xFFB04A))
    ctx.restoreGState()
    ctx.saveGState()
    ctx.setShadow(offset: .zero, blur: s * 0.03, color: rgba(0x000000, 0.7))
    ctx.translateBy(x: s * 0.011, y: -s * 0.004)
    fill(rgba(0x07050A))
    ctx.restoreGState()

    // Soft warm fill-light on the lit side of the figure.
    let rim = CGGradient(colorsSpace: cs, colors: [rgba(0xD84A1E, 0.55), rgba(0x5A1410, 0.3), rgba(0x000000, 0)] as CFArray, locations: [0, 0.18, 0.4])!
    for part in [head, body] {
        ctx.saveGState()
        ctx.translateBy(x: s * 0.011, y: -s * 0.004)
        ctx.addPath(part)
        ctx.clip()
        ctx.drawLinearGradient(rim, start: CGPoint(x: P(30, 0).x, y: 0), end: CGPoint(x: P(100, 0).x, y: 0), options: [])
        ctx.restoreGState()
    }
}

func render(size: Int, scale: Int, to url: URL) {
    let px = size * scale
    let cs = CGColorSpace(name: CGColorSpace.sRGB)!
    guard let ctx = CGContext(data: nil, width: px, height: px, bitsPerComponent: 8, bytesPerRow: 0, space: cs,
                              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return }
    let s = CGFloat(px)
    ctx.clear(CGRect(x: 0, y: 0, width: s, height: s))

    // macOS icon shape: rounded square with ~10% transparent margin.
    let inset = s * 0.09
    let tile = CGRect(x: inset, y: inset, width: s - 2 * inset, height: s - 2 * inset)
    let tilePath = CGPath(roundedRect: tile, cornerWidth: tile.width * 0.225, cornerHeight: tile.width * 0.225, transform: nil)

    // Drop shadow under the tile (only visible at larger sizes).
    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -s * 0.01), blur: s * 0.03, color: rgba(0x000000, 0.45))
    ctx.addPath(tilePath)
    ctx.setFillColor(rgba(0x0B0C10))
    ctx.fillPath()
    ctx.restoreGState()

    ctx.saveGState()
    ctx.addPath(tilePath)
    ctx.clip()
    // Background: near-black with a deep red radial glow low behind the letter.
    let bg = CGGradient(colorsSpace: cs, colors: [rgba(0x151117), rgba(0x08080B)] as CFArray, locations: [0, 1])!
    ctx.drawLinearGradient(bg, start: CGPoint(x: 0, y: s), end: CGPoint(x: 0, y: 0), options: [])
    let glow = CGGradient(colorsSpace: cs, colors: [rgba(0xA8141C, 0.55), rgba(0x5A0A10, 0.25), rgba(0x000000, 0)] as CFArray, locations: [0, 0.45, 1])!
    ctx.drawRadialGradient(glow, startCenter: CGPoint(x: s * 0.5, y: s * 0.42), startRadius: 0, endCenter: CGPoint(x: s * 0.5, y: s * 0.42), endRadius: s * 0.5, options: [])

    if variant == "eye" || variant == "figure" {
        if variant == "eye" { drawEye(ctx, cs: cs, tile: tile, s: s) } else { drawFigure(ctx, cs: cs, tile: tile, s: s) }
        ctx.restoreGState()
        guard let img = ctx.makeImage() else { return }
        let rep = NSBitmapImageRep(cgImage: img)
        rep.size = NSSize(width: size, height: size)
        if let png = rep.representation(using: .png, properties: [:]) { try? png.write(to: url) }
        return
    }

    // The letter: scale the 0...100 box into the tile with margins, lean it
    // forward ~9 degrees, and sit it slightly low like a title card.
    let box = tile.insetBy(dx: tile.width * 0.16, dy: tile.height * 0.17)
    var t = CGAffineTransform(translationX: box.minX - box.width * 0.06, y: box.minY)
    t = t.scaledBy(x: box.width / 100, y: box.height / 100)
    t = t.concatenating(CGAffineTransform(a: 1, b: 0, c: 0.16, d: 1, tx: -0.16 * box.midY + 0.16 * box.minY, ty: 0))
    let letter = letterPath().copy(using: &t)!

    // Glow behind the letter.
    ctx.saveGState()
    ctx.setShadow(offset: .zero, blur: s * 0.06, color: rgba(0xFF3B1F, 0.75))
    ctx.addPath(letter)
    ctx.setFillColor(rgba(0xC8161F))
    ctx.fillPath(using: .evenOdd)
    ctx.restoreGState()

    // Chrome fill: light orange top, hot orange, hard horizon, deep red bottom.
    ctx.saveGState()
    ctx.addPath(letter)
    ctx.clip(using: .evenOdd)
    let chrome = CGGradient(colorsSpace: cs,
        colors: [rgba(0xFFD9A6), rgba(0xFF9A3C), rgba(0xFF5A1F), rgba(0xFF3A1A), rgba(0xA50F1B), rgba(0xD02A1E), rgba(0x6E0912)] as CFArray,
        locations: [0, 0.22, 0.46, 0.5, 0.52, 0.8, 1])!
    ctx.drawLinearGradient(chrome, start: CGPoint(x: 0, y: letter.boundingBox.maxY), end: CGPoint(x: 0, y: letter.boundingBox.minY), options: [])
    // Thin highlight along the top edge for the metal look.
    let edge = CGGradient(colorsSpace: cs, colors: [rgba(0xFFFFFF, 0.55), rgba(0xFFFFFF, 0)] as CFArray, locations: [0, 1])!
    ctx.drawLinearGradient(edge, start: CGPoint(x: 0, y: letter.boundingBox.maxY), end: CGPoint(x: 0, y: letter.boundingBox.maxY - letter.boundingBox.height * 0.08), options: [])
    ctx.restoreGState()

    // Fine dark outline so the letter reads at small sizes.
    ctx.addPath(letter)
    ctx.setStrokeColor(rgba(0x2A0508, 0.9))
    ctx.setLineWidth(max(1, s * 0.004))
    ctx.strokePath()
    ctx.restoreGState()

    guard let img = ctx.makeImage() else { return }
    let rep = NSBitmapImageRep(cgImage: img)
    rep.size = NSSize(width: size, height: size)
    guard let png = rep.representation(using: .png, properties: [:]) else { return }
    try? png.write(to: url)
}

let out = URL(fileURLWithPath: outDir)
for size in [16, 32, 128, 256, 512] {
    render(size: size, scale: 1, to: out.appendingPathComponent("icon_\(size)x\(size).png"))
    render(size: size, scale: 2, to: out.appendingPathComponent("icon_\(size)x\(size)@2x.png"))
}
render(size: 512, scale: 1, to: out.appendingPathComponent("preview.png"))
print("icon pngs written to \(outDir)")
