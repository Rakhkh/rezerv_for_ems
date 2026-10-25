#ifndef MSGFMT_UTF8_HPP
#define MSGFMT_UTF8_HPP

#include <cstddef>
#include <optional>
#include <string_view>

namespace msgfmt::detail {

struct DecodedCodePoint {
    char32_t value;
    std::size_t byte_length;
};

// Reads one scalar value without changing input. Returns nullopt for invalid
// UTF-8 or an offset at/past the end. Bytes before offset are not inspected.
[[nodiscard]] std::optional<DecodedCodePoint> decode_utf8(
    std::string_view input, std::size_t offset) noexcept;

// Precondition: value is a Unicode scalar value obtained from valid UTF-8.
[[nodiscard]] std::size_t utf16_width(char32_t value) noexcept;

// Validates the entire input. Empty input has length zero; invalid UTF-8 has
// no length. No normalization or newline/control-character conversion occurs.
[[nodiscard]] std::optional<std::size_t> utf16_length(std::string_view input) noexcept;

// Fixed classification from PLAN_task1.md; independent of the system locale.
[[nodiscard]] bool is_letter(char32_t value) noexcept;
[[nodiscard]] bool is_digit(char32_t value) noexcept;
[[nodiscard]] bool is_whitespace(char32_t value) noexcept;

} // namespace msgfmt::detail

#endif // MSGFMT_UTF8_HPP
