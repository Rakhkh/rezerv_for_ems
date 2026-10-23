#include "test_support.hpp"
#include "utf8.hpp"

#include <algorithm>
#include <variant>

namespace msgfmt::tests {

std::string bytes(std::initializer_list<unsigned int> values)
{
    std::string result;
    for (const auto value : values) {
        result.push_back(static_cast<char>(value));
    }
    return result;
}

// Error categories for public API and CLI validation; exhaustive malformed
// sequences and boundary variants are checked directly against the decoder.
std::vector<InvalidCase> representative_invalid_inputs()
{
    return {
        {"isolated continuation 80", bytes({0x80})},
        {"overlong E0 lower", bytes({0xE0, 0x80, 0x80})},
        {"first surrogate", bytes({0xED, 0xA0, 0x80})},
        {"U+110000", bytes({0xF4, 0x90, 0x80, 0x80})},
        {"three-byte bad third", bytes({0xE2, 0x82, 0x20})},
        {"truncated four-byte 3", bytes({0xF0, 0x90, 0x80})},
    };
}

std::vector<InvalidCase> invalid_inputs()
{
    auto result = representative_invalid_inputs();
    result.insert(result.end(), {
        {"isolated continuation BF", bytes({0xBF})},
        {"overlong C0", bytes({0xC0, 0xAF})},
        {"overlong C1", bytes({0xC1, 0xBF})},
        {"overlong E0 upper", bytes({0xE0, 0x9F, 0xBF})},
        {"overlong F0 lower", bytes({0xF0, 0x80, 0x80, 0x80})},
        {"overlong F0 upper", bytes({0xF0, 0x8F, 0xBF, 0xBF})},
        {"last surrogate", bytes({0xED, 0xBF, 0xBF})},
        {"F4 above Unicode", bytes({0xF4, 0xBF, 0xBF, 0xBF})},
        {"F5 lead", bytes({0xF5, 0x80, 0x80, 0x80})},
        {"five-byte form", bytes({0xF8, 0x88, 0x80, 0x80, 0x80})},
        {"six-byte form", bytes({0xFC, 0x84, 0x80, 0x80, 0x80, 0x80})},
        {"FE lead", bytes({0xFE})},
        {"FF lead", bytes({0xFF})},
        {"two-byte bad continuation", bytes({0xC2, 0x20})},
        {"three-byte bad second", bytes({0xE2, 0x28, 0xA1})},
        {"four-byte bad second", bytes({0xF0, 0x28, 0x80, 0x80})},
        {"four-byte bad third", bytes({0xF0, 0x90, 0x20, 0x80})},
        {"four-byte bad fourth", bytes({0xF0, 0x90, 0x80, 0x20})},
        {"NUL continuation", bytes({0xC2, 0x00})},
        {"lead used as continuation", bytes({0xE2, 0xC2, 0xA1})},
        {"truncated two-byte", bytes({0xC2})},
        {"truncated three-byte 1", bytes({0xE2})},
        {"truncated three-byte 2", bytes({0xE2, 0x82})},
        {"truncated four-byte 1", bytes({0xF0})},
        {"truncated four-byte 2", bytes({0xF0, 0x90})},
    });
    return result;
}

// Independent test encoder: production code only decodes and preserves bytes.
std::string encode_scalar(char32_t value)
{
    if (value <= 0x7F) {
        return bytes({static_cast<unsigned int>(value)});
    }
    if (value <= 0x7FF) {
        return bytes({0xC0U | (value >> 6), 0x80U | (value & 0x3FU)});
    }
    if (value <= 0xFFFF) {
        return bytes({0xE0U | (value >> 12), 0x80U | ((value >> 6) & 0x3FU),
                      0x80U | (value & 0x3FU)});
    }
    return bytes({0xF0U | (value >> 18), 0x80U | ((value >> 12) & 0x3FU),
                  0x80U | ((value >> 6) & 0x3FU), 0x80U | (value & 0x3FU)});
}

namespace {

void check_range_invariants(TestContext& context, const msgfmt::ParsedMessage& message,
                            std::string_view name)
{
    const auto length = msgfmt::detail::utf16_length(message.text);
    context.check(length.has_value(), "plain text remains valid UTF-8", name);
    if (!length) {
        return;
    }
    std::vector<bool> boundaries(*length + 1, false);
    boundaries[0] = true;
    std::size_t byte_offset = 0;
    std::size_t unit_offset = 0;
    while (byte_offset < message.text.size()) {
        const auto point = msgfmt::detail::decode_utf8(message.text, byte_offset);
        byte_offset += point->byte_length;
        unit_offset += msgfmt::detail::utf16_width(point->value);
        boundaries[unit_offset] = true;
    }

    std::vector<std::size_t> enclosing_ends;
    for (std::size_t i = 0; i < message.ranges.size(); ++i) {
        const auto& range = message.ranges[i];
        const bool in_bounds = range.offset <= *length && range.length <= *length - range.offset;
        context.check(in_bounds && range.length > 0, "nonempty range inside plain text", name);
        if (!in_bounds) {
            continue;
        }
        const auto end = range.offset + range.length;
        context.check(boundaries[range.offset] && boundaries[end], "do not split surrogate pairs",
                      name);
        context.check((range.style == msgfmt::Style::link) != range.url.empty(),
                      "only links have a nonempty URL", name);
        context.check(msgfmt::detail::utf16_length(range.url).has_value(), "URL remains valid UTF-8",
                      name);
        if (i > 0) {
            const auto& previous = message.ranges[i - 1];
            context.check(previous.offset < range.offset
                              || (previous.offset == range.offset && previous.length >= range.length),
                          "ranges sorted by offset and descending length", name);
        }
        while (!enclosing_ends.empty() && range.offset >= enclosing_ends.back()) {
            enclosing_ends.pop_back();
        }
        context.check(enclosing_ends.empty() || end <= enclosing_ends.back(),
                      "ranges do not partially overlap", name);
        enclosing_ends.push_back(end);
    }
}

} // namespace

void check_parse(TestContext& context, const ParseCase& item)
{
    const auto result = msgfmt::parse(item.input);
    const auto* message = std::get_if<msgfmt::ParsedMessage>(&result);
    context.check(message != nullptr, "parse accepts valid UTF-8", item.name);
    if (!message) {
        return;
    }
    context.check(message->text == item.text, "exact plain text", item.name);
    context.check(message->ranges.size() == item.ranges.size(), "exact range count", item.name);
    const auto count = std::min(message->ranges.size(), item.ranges.size());
    for (std::size_t i = 0; i < count; ++i) {
        const auto& actual = message->ranges[i];
        const auto& expected = item.ranges[i];
        context.check(actual.style == expected.style && actual.offset == expected.offset
                          && actual.length == expected.length && actual.url == expected.url,
                      "exact range fields and opening order", item.name);
    }
    check_range_invariants(context, *message, item.name);
}

std::vector<ParseCase> control_examples()
{
    using msgfmt::Style;
    return {
        {"A1", u8"**важно**", u8"важно", {{Style::bold, 0, 5, ""}}},
        {"A2", u8"Привет, **мир**!", u8"Привет, мир!", {{Style::bold, 8, 3, ""}}},
        {"A3", u8"_курсив_", u8"курсив", {{Style::italic, 0, 6, ""}}},
        {"A4", "a_b_c", "a_b_c", {}},
        {"A5", u8"**не закрыто", u8"**не закрыто", {}},
        {"A6", R"(2 \* 3)", "2 * 3", {}},
        {"A7", u8"`чат * список`", u8"чат * список", {{Style::code, 0, 12, ""}}},
        {"A8", u8"[документация](https://docs.github.com)", u8"документация",
         {{Style::link, 0, 12, "https://docs.github.com"}}},
        // The agreed correction to the specification's contradictory example 9.
        {"A9 corrected", u8"**жирный _и курсив_**", u8"жирный и курсив",
         {{Style::bold, 0, 15, ""}, {Style::italic, 7, 8, ""}}},
        {"A10", u8"🚀 **ок**", u8"🚀 ок", {{Style::bold, 3, 2, ""}}},
        {"A11", u8"**🚀 ок**", u8"🚀 ок", {{Style::bold, 0, 5, ""}}},
        {"A12", u8"[текст]()", u8"[текст]()", {}},
        {"A13", R"(\\)", R"(\)", {}},
        {"A14", "", "", {}},
        {"A15", "_ e_", "_ e_", {}},
    };
}

} // namespace msgfmt::tests
