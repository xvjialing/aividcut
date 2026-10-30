// 仅生成自有OCR验收图片，不参与产品构建或处理客户素材。
import AppKit
let folder = CommandLine.arguments[1]
func save(_ name:String,_ lines:[String]) throws {
 let image=NSImage(size:NSSize(width:960,height:540))
 image.lockFocus()
 NSColor.white.setFill();NSRect(x:0,y:0,width:960,height:540).fill()
 for (index,text) in lines.enumerated() {
  let attributes:[NSAttributedString.Key:Any] = [.font:NSFont.systemFont(ofSize:index==0 ? 52:36),.foregroundColor:NSColor.black]
  (text as NSString).draw(at:NSPoint(x:55,y:360-index*100),withAttributes:attributes)
 }
 image.unlockFocus()
 let bitmap=NSBitmapImageRep(data:image.tiffRepresentation!)!
 try bitmap.representation(using:.png,properties:[:])!.write(to:URL(fileURLWithPath:folder+"/"+name))
}
try save("中英文源画面.png",["AIVidCut 本地文字识别", "SOURCE FRAME 120", "这是源画面，不是自动字幕"])
try save("无文字源画面.png",[])

for second in 0...3 {
 try save("计数-\(second).png",["AIVidCut 本地文字识别","SOURCE FRAME 120","这是源画面，不是自动字幕","SOURCE SECOND \(second)"])
}
