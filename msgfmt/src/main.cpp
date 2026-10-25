#include "msgfmt.hpp"

#include <algorithm>
#include <array>
#include <charconv>
#include <csignal>
#include <cstdio>
#include <iostream>
#include <limits>
#include <string>
#include <string_view>
#include <utility>

#if defined(_WIN32) && !defined(MSGFMT_CLI_TESTING)
#include <fcntl.h>
#include <io.h>
#endif

namespace msgfmt::cli {
namespace {

void append_json_string(std::string& output, std::string_view text)
{
    constexpr char hex[] = "0123456789abcdef";
    output.push_back('"');
    for (const unsigned char byte : text) {
        switch (byte) {
        case '"': output += "\\\""; break;
        case '\\': output += "\\\\"; break;
        case '\b': output += "\\b"; break;
        case '\f': output += "\\f"; break;
        case '\n': output += "\\n"; break;
        case '\r': output += "\\r"; break;
        case '\t': output += "\\t"; break;
        default:
            if (byte < 0x20) {
                output += "\\u00";
                output.push_back(hex[byte >> 4]);
                output.push_back(hex[byte & 0x0F]);
            } else {
                output.push_back(static_cast<char>(byte));
            }
        }
    }
    output.push_back('"');
}

void append_number(std::string& output, std::size_t value)
{
    // digits10 + 1 fits every size_t, so to_chars cannot exhaust this buffer.
    std::array<char, std::numeric_limits<std::size_t>::digits10 + 1> buffer{};
    const auto converted = std::to_chars(buffer.data(), buffer.data() + buffer.size(), value);
    output.append(buffer.data(), static_cast<std::size_t>(converted.ptr - buffer.data()));
}

std::string_view style_name(Style style) noexcept
{
    switch (style) {
    case Style::bold: return "bold";
    case Style::italic: return "italic";
    case Style::code: return "code";
    case Style::link: return "link";
    }
    return {};
}

std::string serialize(const ParsedMessage& message)
{
    std::string output = "{\"text\":";
    append_json_string(output, message.text);
    output += ",\"ranges\":[";
    bool first = true;
    for (const auto& range : message.ranges) {
        if (!first) {
            output.push_back(',');
        }
        first = false;
        output += "{\"style\":";
        append_json_string(output, style_name(range.style));
        output += ",\"offset\":";
        append_number(output, range.offset);
        output += ",\"length\":";
        append_number(output, range.length);
        output += ",\"url\":";
        append_json_string(output, range.url);
        output.push_back('}');
    }
    output += "]}";
    return output;
}

} // namespace

// Internal entry point shared with stream-failure tests; not a library API.
int run(std::istream& input, std::ostream& output, std::ostream& error, bool escape_mode)
{
    std::string bytes;
    std::array<char, 4096> buffer{};
    while (input) {
        input.read(buffer.data(), static_cast<std::streamsize>(buffer.size()));
        bytes.append(buffer.data(), static_cast<std::size_t>(input.gcount()));
    }
    if (input.bad() || !input.eof()) {
        error << "msgfmt: failed to read stdin\n";
        return 1;
    }

    std::string result;
    int status = 0;
    if (escape_mode) {
        auto escaped = escape(bytes);
        if (auto* text = std::get_if<std::string>(&escaped)) {
            result = std::move(*text);
        } else {
            status = 2;
        }
    } else {
        const auto parsed = parse(bytes);
        if (const auto* message = std::get_if<ParsedMessage>(&parsed)) {
            result = serialize(*message);
        } else {
            status = 2;
        }
    }
    if (status == 2) {
        result = "{\"error\":\"invalid_utf8\"}";
    }

    // Flush explicitly: buffered output failures must affect the exit code.
    for (std::size_t offset = 0; offset < result.size();) {
        const auto count = std::min(buffer.size(), result.size() - offset);
        output.write(result.data() + offset, static_cast<std::streamsize>(count));
        if (!output) {
            error << "msgfmt: failed to write stdout\n";
            return 1;
        }
        offset += count;
    }
    output.flush();
    if (!output) {
        error << "msgfmt: failed to write stdout\n";
        return 1;
    }
    return status;
}

} // namespace msgfmt::cli

#ifndef MSGFMT_CLI_TESTING
int main(int argc, char* argv[])
{
#ifdef _WIN32
    if (_setmode(_fileno(stdin), _O_BINARY) == -1
        || _setmode(_fileno(stdout), _O_BINARY) == -1
        || _setmode(_fileno(stderr), _O_BINARY) == -1) {
        std::cerr << "msgfmt: failed to enable binary I/O\n";
        return 1;
    }
#endif
    if (argc > 2 || (argc == 2 && std::string_view(argv[1]) != "escape")) {
        std::cerr << "Usage: msgfmt [escape]\n";
        return 1;
    }
#if !defined(_WIN32) && defined(SIGPIPE)
    // Let a broken pipe become a checked output error instead of a signal exit.
    if (std::signal(SIGPIPE, SIG_IGN) == SIG_ERR) {
        std::cerr << "msgfmt: failed to configure output error handling\n";
        return 1;
    }
#endif
    return msgfmt::cli::run(std::cin, std::cout, std::cerr, argc == 2);
}
#endif
