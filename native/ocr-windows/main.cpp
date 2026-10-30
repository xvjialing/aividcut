// 仅接收stdin JPEG，不接受工程、路径或网络参数；进程隔离不是OS权限沙箱。
#define NOMINMAX
#include <Windows.h>
#include <appmodel.h>
#include <fcntl.h>
#include <io.h>
#include <iostream>
#include <string>
#include <vector>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.Globalization.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Graphics.Imaging.h>
#include <winrt/Windows.Media.Ocr.h>
#include <winrt/Windows.Storage.Streams.h>

using namespace winrt;
using namespace Windows::Data::Json;
using namespace Windows::Graphics::Imaging;
using namespace Windows::Media::Ocr;
using namespace Windows::Storage::Streams;

static int fail(char const* code) {
    std::cout << "{\"errorCode\":\"" << code << "\"}";
    return 1;
}

// 按Unicode码点截断，不能把UTF16代理对截成半个字符。
static hstring clipped(hstring const& text, bool& truncated) {
    std::wstring value(text.c_str(), text.size());
    size_t offset = 0, count = 0;
    while (offset < value.size() && count < 1000) {
        wchar_t c = value[offset++];
        if (c >= 0xD800 && c <= 0xDBFF && offset < value.size() && value[offset] >= 0xDC00 && value[offset] <= 0xDFFF) ++offset;
        ++count;
    }
    if (offset < value.size()) truncated = true;
    return hstring(value.substr(0, offset));
}

int wmain(int argc, wchar_t** argv) {
    bool probe = argc == 2 && std::wstring(argv[1]) == L"--probe";
    if (argc != 1 && !probe) return fail("invalid_request");
    UINT32 length = 0;
    if (GetCurrentPackageFullName(&length, nullptr) != ERROR_INSUFFICIENT_BUFFER || length == 0) return fail("package_identity_required");
    try {
        init_apartment(apartment_type::multi_threaded);
        auto engine = OcrEngine::TryCreateFromUserProfileLanguages();
        if (!engine) return fail("no_language");
        JsonObject result;
        result.Insert(L"version", JsonValue::CreateNumberValue(1));
        result.Insert(L"engine", JsonValue::CreateStringValue(L"windows-media-ocr"));
        // Windows接口不公开模型修订，0表示未知，不是识别器版本号。
        result.Insert(L"revision", JsonValue::CreateNumberValue(0));
        JsonArray languages;
        languages.Append(JsonValue::CreateStringValue(engine.RecognizerLanguage().LanguageTag()));
        result.Insert(L"languages", languages);
        JsonArray lines;
        bool truncated = false;
        if (!probe) {
            _setmode(_fileno(stdin), _O_BINARY);
            std::vector<uint8_t> data;
            constexpr size_t limit = 4 * 1024 * 1024;
            char block[65536];
            while (std::cin) {
                std::cin.read(block, sizeof(block));
                size_t read = static_cast<size_t>(std::cin.gcount());
                if (data.size() + read > limit) return fail("invalid_request");
                data.insert(data.end(), block, block + read);
            }
            if (data.size() < 2 || data[0] != 0xff || data[1] != 0xd8) return fail("invalid_request");
            InMemoryRandomAccessStream stream;
            DataWriter writer(stream);
            writer.WriteBytes(array_view<uint8_t const>(data.data(), data.data() + data.size()));
            writer.StoreAsync().get();
            writer.FlushAsync().get();
            writer.DetachStream();
            stream.Seek(0);
            auto decoder = BitmapDecoder::CreateAsync(stream).get();
            if (decoder.PixelWidth() == 0 || decoder.PixelHeight() == 0 || decoder.PixelWidth() > 960 || decoder.PixelHeight() > 540 || decoder.PixelWidth() > OcrEngine::MaxImageDimension() || decoder.PixelHeight() > OcrEngine::MaxImageDimension()) return fail("invalid_request");
            auto bitmap = decoder.GetSoftwareBitmapAsync(BitmapPixelFormat::Bgra8, BitmapAlphaMode::Premultiplied).get();
            auto recognized = engine.RecognizeAsync(bitmap).get();
            truncated = recognized.Lines().Size() > 50;
            for (auto const& line : recognized.Lines()) {
                if (lines.Size() >= 50) break;
                if (line.Text().empty()) continue;
                JsonObject entry;
                entry.Insert(L"text", JsonValue::CreateStringValue(clipped(line.Text(), truncated)));
                // OcrLine没有置信度接口，必须明确为空。
                entry.Insert(L"confidence", JsonValue::CreateNullValue());
                lines.Append(entry);
            }
        }
        result.Insert(L"lines", lines);
        result.Insert(L"truncated", JsonValue::CreateBooleanValue(truncated));
        std::cout << to_string(result.Stringify());
        return 0;
    } catch (...) {
        return fail("recognition_failed");
    }
}
