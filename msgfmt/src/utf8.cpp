#include "utf8.hpp"

namespace msgfmt::detail {

std::optional<DecodedCodePoint> decode_utf8(std::string_view input, std::size_t offset) noexcept
{
    if (offset >= input.size()) {
        return std::nullopt;
    }

    const auto first = static_cast<unsigned char>(input[offset]);
    if (first <= 0x7F) {
        return DecodedCodePoint{first, 1};
    }

    std::size_t byte_length;
    char32_t value;
    char32_t minimum;
    if (first >= 0xC2 && first <= 0xDF) {
        byte_length = 2;
        value = first & 0x1F;
        minimum = 0x80;
    } else if (first >= 0xE0 && first <= 0xEF) {
        byte_length = 3;
        value = first & 0x0F;
        minimum = 0x800;
    } else if (first >= 0xF0 && first <= 0xF4) {
        byte_length = 4;
        value = first & 0x07;
        minimum = 0x10000;
    } else {
        return std::nullopt;
    }

    // Subtraction avoids overflow for an arbitrary caller-supplied offset.
    if (input.size() - offset < byte_length) {
        return std::nullopt;
    }
    for (std::size_t i = 1; i < byte_length; ++i) {
        const auto next = static_cast<unsigned char>(input[offset + i]);
        if ((next & 0xC0) != 0x80) {
            return std::nullopt;
        }
        value = (value << 6) | (next & 0x3F);
    }

    // Reject overlong forms, surrogate code points and values beyond Unicode.
    // Unicode 17.0, section 3.9.3, Table 3-7.
    if (value < minimum || value > 0x10FFFF || (value >= 0xD800 && value <= 0xDFFF)) {
        return std::nullopt;
    }
    return DecodedCodePoint{value, byte_length};
}

std::size_t utf16_width(char32_t value) noexcept
{
    return value <= 0xFFFF ? 1 : 2;
}

std::optional<std::size_t> utf16_length(std::string_view input) noexcept
{
    std::size_t length = 0;
    std::size_t offset = 0;
    while (offset < input.size()) {
        const auto decoded = decode_utf8(input, offset);
        if (!decoded) {
            return std::nullopt;
        }
        length += utf16_width(decoded->value);
        offset += decoded->byte_length;
    }
    // Each code point uses at least as many UTF-8 bytes as UTF-16 units, so
    // length never exceeds input.size() and cannot overflow std::size_t.
    return length;
}

bool is_letter(char32_t value) noexcept
{
    return (value >= U'A' && value <= U'Z') || (value >= U'a' && value <= U'z')
        || (value >= 0x0400 && value <= 0x0481) || (value >= 0x048A && value <= 0x04FF);
}

bool is_digit(char32_t value) noexcept
{
    return value >= U'0' && value <= U'9';
}

bool is_whitespace(char32_t value) noexcept
{
    // Unicode 17.0 PropList.txt, White_Space (25 code points).
    return (value >= 0x0009 && value <= 0x000D) || value == 0x0020 || value == 0x0085
        || value == 0x00A0 || value == 0x1680 || (value >= 0x2000 && value <= 0x200A)
        || value == 0x2028 || value == 0x2029 || value == 0x202F || value == 0x205F
        || value == 0x3000;
}

} // namespace msgfmt::detail
