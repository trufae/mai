package main

import (
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"image"
	"image/color"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type VolumeButton struct {
	Clickable widget.Clickable
	Label     string
	Color     color.NRGBA
	Inactive  bool
}

func (vb *VolumeButton) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	// Colors
	bottomColor := color.NRGBA{
		R: uint8(max(0, int(vb.Color.R)-80)),
		G: uint8(max(0, int(vb.Color.G)-80)),
		B: uint8(max(0, int(vb.Color.B)-80)),
		A: vb.Color.A,
	}
	topColor := vb.Color
	if vb.Inactive {
		bottomColor = color.NRGBA{R: 128, G: 128, B: 128, A: 255}
		topColor = color.NRGBA{R: 160, G: 160, B: 160, A: 255}
	}

	// Size
	width := gtx.Dp(unit.Dp(60))
	height := gtx.Dp(unit.Dp(40))
	size := image.Point{X: width, Y: height}

	// Layout function
	layoutFunc := func(gtx layout.Context) layout.Dimensions {
		// Draw top rect
		offsetY := 0
		if vb.Clickable.Pressed() {
			offsetY = gtx.Dp(unit.Dp(5))
		}
		// Draw bottom rect
		bottomRect := image.Rect(0, offsetY, size.X, size.Y)
		radius := gtx.Dp(unit.Dp(10))
		rrectBottom := clip.RRect{Rect: bottomRect, SE: radius, SW: radius, NE: radius, NW: radius}
		pathBottom := rrectBottom.Path(gtx.Ops)
		strokeBottom := clip.Stroke{Path: pathBottom, Width: float32(gtx.Dp(unit.Dp(3)))}.Op()
		paint.FillShape(gtx.Ops, bottomColor, strokeBottom)

		topRect := image.Rect(0, offsetY, size.X, offsetY+size.Y-gtx.Dp(unit.Dp(10)))
		rrectTop := clip.RRect{Rect: topRect, SE: radius, SW: radius, NE: radius, NW: radius}
		pathTop := rrectTop.Path(gtx.Ops)
		strokeTop := clip.Stroke{Path: pathTop, Width: float32(gtx.Dp(unit.Dp(3)))}.Op()
		paint.FillShape(gtx.Ops, topColor, strokeTop)

		// Label
		label := material.Body1(th, vb.Label)
		var inset layout.Inset
		if vb.Clickable.Pressed() {
			inset = layout.Inset{Top: unit.Dp(10), Left: unit.Dp(20), Right: unit.Dp(4)}
		} else {
			inset = layout.Inset{Top: unit.Dp(5), Left: unit.Dp(20), Right: unit.Dp(4)}
		}
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: size}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return inset.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, label.Layout)
				})
			}),
		)
	}

	if vb.Inactive {
		return layoutFunc(gtx)
	} else {
		return vb.Clickable.Layout(gtx, layoutFunc)
	}
}
