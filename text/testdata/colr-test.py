# Builds colr-test.ttf: a COLRv1 font with a wght axis for the tests of the text module.
#   A: SrcIn of a red square in a circle mask
#   B: a red square inside a circle glyph
#   C: a red circle whose alpha falls from 1 to 0.5 at wght 700
# Run with fontTools 4.x: python colr-test.py
from fontTools.colorLib.builder import buildCOLR, buildCPAL
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.ttLib.tables import otTables as ot
from fontTools.varLib.varStore import OnlineVarStoreBuilder


def square():
    pen = TTGlyphPen(None)
    pen.moveTo((0, 0))
    pen.lineTo((0, 800))
    pen.lineTo((800, 800))
    pen.lineTo((800, 0))
    pen.closePath()
    return pen.glyph()


def circle():
    pen = TTGlyphPen(None)
    pen.moveTo((400, 0))
    pen.qCurveTo((0, 0), (0, 400))
    pen.qCurveTo((0, 800), (400, 800))
    pen.qCurveTo((800, 800), (800, 400))
    pen.qCurveTo((800, 0), (400, 0))
    pen.closePath()
    return pen.glyph()


order = [".notdef", "A", "B", "C", "square", "circle"]
fb = FontBuilder(1000, isTTF=True)
fb.setupGlyphOrder(order)
fb.setupCharacterMap({ord("A"): "A", ord("B"): "B", ord("C"): "C"})
empty = TTGlyphPen(None).glyph()
fb.setupGlyf({".notdef": empty, "A": empty, "B": empty, "C": empty, "square": square(), "circle": circle()})
fb.setupHorizontalMetrics({g: (1000, 0) for g in order})
fb.setupHorizontalHeader(ascent=800, descent=-200)
fb.setupNameTable({"familyName": "COLR Test", "styleName": "Regular"})
fb.setupOS2()
fb.setupPost()
fb.setupFvar([("wght", 400, 400, 700, "Weight")], [])

store = OnlineVarStoreBuilder(["wght"])
store.setSupports([{"wght": (0, 1, 1)}])
alpha = store.storeDeltas([-8192])  # -0.5 in units of 1/16384
red = {"Format": ot.PaintFormat.PaintSolid, "PaletteIndex": 0, "Alpha": 1.0}
fb.font["COLR"] = buildCOLR(
    {
        "A": {
            "Format": ot.PaintFormat.PaintComposite,
            "CompositeMode": "src_in",
            "SourcePaint": {"Format": ot.PaintFormat.PaintGlyph, "Glyph": "square", "Paint": red},
            "BackdropPaint": {"Format": ot.PaintFormat.PaintGlyph, "Glyph": "circle", "Paint": red},
        },
        "B": {
            "Format": ot.PaintFormat.PaintGlyph,
            "Glyph": "circle",
            "Paint": {"Format": ot.PaintFormat.PaintGlyph, "Glyph": "square", "Paint": red},
        },
        "C": {
            "Format": ot.PaintFormat.PaintGlyph,
            "Glyph": "circle",
            "Paint": {"Format": ot.PaintFormat.PaintVarSolid, "PaletteIndex": 0, "Alpha": 1.0, "VarIndexBase": alpha},
        },
    },
    version=1,
    varStore=store.finish(),
)
fb.font["CPAL"] = buildCPAL([[(1.0, 0.0, 0.0, 1.0)]])
fb.save("colr-test.ttf")
