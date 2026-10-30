import Foundation
import Vision

// 独立进程仅从标准输入读取有界JPEG，不接收路径、URL或云端凭据。
struct OCRLine: Codable { let text: String; let confidence: Float }
struct OCRReply: Codable {
    let version: Int
    let engine: String
    let revision: Int
    let languages: [String]
    let lines: [OCRLine]
    let truncated: Bool
}
func run() throws {
    let request = VNRecognizeTextRequest()
    request.revision = VNRecognizeTextRequestRevision3
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = false
    request.minimumTextHeight = 0.01
    let supported = try request.supportedRecognitionLanguages()
    let languages = ["zh-Hans", "zh-Hant", "en-US"].filter { supported.contains($0) }
    guard languages.contains("zh-Hans") && languages.contains("en-US") else {
        throw NSError(domain: "AIVidCutOCR", code: 1, userInfo: [NSLocalizedDescriptionKey: "当前系统缺少要求的中英文识别能力"])
    }
    request.recognitionLanguages = languages
    var lines: [OCRLine] = []
    var truncated = false
    if CommandLine.arguments != [CommandLine.arguments[0], "--probe"] {
        guard CommandLine.arguments.count == 1 else { throw NSError(domain: "AIVidCutOCR", code: 2) }
        let limit = 4 * 1024 * 1024
        var data = Data()
        while data.count <= limit {
            let part = try FileHandle.standardInput.read(upToCount: min(65536, limit + 1 - data.count)) ?? Data()
            if part.isEmpty { break }
            data.append(part)
        }
        guard data.count > 0 && data.count <= limit else { throw NSError(domain: "AIVidCutOCR", code: 3) }
        let handler = VNImageRequestHandler(data: data, orientation: .up, options: [:])
        try handler.perform([request])
        let observations = (request.results ?? []).sorted {
            let dy = $0.boundingBox.midY - $1.boundingBox.midY
            return dy != 0 ? dy > 0 : $0.boundingBox.minX < $1.boundingBox.minX
        }
        truncated = observations.count > 50
        for observation in observations.prefix(50) {
            guard let candidate = observation.topCandidates(1).first else { continue }
            let original = candidate.string.trimmingCharacters(in: .whitespacesAndNewlines)
            if original.isEmpty { continue }
            if original.count > 1000 { truncated = true }
            lines.append(OCRLine(text: String(original.prefix(1000)), confidence: candidate.confidence))
        }
    }
    let reply = OCRReply(version: 1, engine: "apple-vision", revision: request.revision,
                         languages: languages, lines: lines, truncated: truncated)
    let encoded = try JSONEncoder().encode(reply)
    FileHandle.standardOutput.write(encoded)
}
do { try autoreleasepool { try run() } }
catch {
    FileHandle.standardError.write(Data("本机画面文字识别失败：\(error.localizedDescription)\n".utf8))
    exit(1)
}
