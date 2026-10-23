#include "msgfmt.hpp"
#include "utf8.hpp"

#include <algorithm>
#include <array>
#include <filesystem>
#include <fstream>
#include <initializer_list>
#include <iostream>
#include <limits>
#include <locale>
#include <sstream>
#include <stdexcept>
#include <streambuf>
#include <string>
#include <string_view>
#include <type_traits>
#include <utility>
#include <variant>
#include <vector>

// Private CLI entry point from main.cpp, linked without its process main.
namespace msgfmt::cli {
int run(std::istream& input, std::ostream& output, std::ostream& error, bool escape_mode);
}

static_assert(__cplusplus >= 201703L, "Tests require C++17");
static_assert(std::is_same_v<decltype(&msgfmt::parse),
                             msgfmt::ParseResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(&msgfmt::escape),
                             msgfmt::EscapeResult (*)(std::string_view)>);
static_assert(std::is_same_v<decltype(msgfmt::Range::offset), std::size_t>);
static_assert(std::is_same_v<decltype(msgfmt::Range::length), std::size_t>);

namespace {

struct TestContext {
    std::size_t checks = 0;
    std::size_t failures = 0;

    void check(bool condition, std::string_view description, std::string_view case_name = {})
    {
        ++checks;
        if (!condition) {
            ++failures;
            if (failures <= 20) {
                std::cerr << "FAIL: " << description;
                if (!case_name.empty()) {
                    std::cerr << " [" << case_name << ']';
                }
                std::cerr << '\n';
            }
        }
    }
};

std::string bytes(std::initializer_list<unsigned int> values)
{
    std::string result;
    for (const auto value : values) {
        result.push_back(static_cast<char>(value));
    }
    return result;
}

struct InvalidCase {
    std::string_view name;
    std::string input;
};

std::vector<InvalidCase> invalid_inputs()
{
    return {
        {"isolated continuation 80", bytes({0x80})},
        {"isolated continuation BF", bytes({0xBF})},
        {"overlong C0", bytes({0xC0, 0xAF})},
        {"overlong C1", bytes({0xC1, 0xBF})},
        {"overlong E0 lower", bytes({0xE0, 0x80, 0x80})},
        {"overlong E0 upper", bytes({0xE0, 0x9F, 0xBF})},
        {"overlong F0 lower", bytes({0xF0, 0x80, 0x80, 0x80})},
        {"overlong F0 upper", bytes({0xF0, 0x8F, 0xBF, 0xBF})},
        {"first surrogate", bytes({0xED, 0xA0, 0x80})},
        {"last surrogate", bytes({0xED, 0xBF, 0xBF})},
        {"U+110000", bytes({0xF4, 0x90, 0x80, 0x80})},
        {"F4 above Unicode", bytes({0xF4, 0xBF, 0xBF, 0xBF})},
        {"F5 lead", bytes({0xF5, 0x80, 0x80, 0x80})},
        {"five-byte form", bytes({0xF8, 0x88, 0x80, 0x80, 0x80})},
        {"six-byte form", bytes({0xFC, 0x84, 0x80, 0x80, 0x80, 0x80})},
        {"FE lead", bytes({0xFE})},
        {"FF lead", bytes({0xFF})},
        {"two-byte bad continuation", bytes({0xC2, 0x20})},
        {"three-byte bad second", bytes({0xE2, 0x28, 0xA1})},
        {"three-byte bad third", bytes({0xE2, 0x82, 0x20})},
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
        {"truncated four-byte 3", bytes({0xF0, 0x90, 0x80})},
    };
}

void test_public_api(TestContext& context)
{
    // Exercise the public value contract without calling the pending parser.
    std::string source = u8"\U0001F680 \u043E\u043A";
    msgfmt::ParsedMessage message{
        source,
        {{msgfmt::Style::bold, 3, 2, ""}},
    };
    source.clear();
    context.check(message.text == u8"\U0001F680 \u043E\u043A", "message owns UTF-8 text");
    context.check(message.ranges.size() == 1, "message stores ranges");
    if (!message.ranges.empty()) {
        const auto& range = message.ranges.front();
        context.check(range.style == msgfmt::Style::bold && range.offset == 3 && range.length == 2
                          && range.url.empty(),
                      "range represents style, UTF-16 coordinates and URL");
    }

    const msgfmt::ParseResult parsed = std::move(message);
    context.check(std::holds_alternative<msgfmt::ParsedMessage>(parsed), "parse success value");
    const msgfmt::ParseResult parse_error = msgfmt::Error::invalid_utf8;
    context.check(std::get<msgfmt::Error>(parse_error) == msgfmt::Error::invalid_utf8,
                  "parse UTF-8 error value");

    const msgfmt::EscapeResult escaped = std::string("a\0b", 3);
    context.check(std::get<std::string>(escaped).size() == 3, "escape result can own NUL bytes");
    const msgfmt::EscapeResult escape_error = msgfmt::Error::invalid_utf8;
    context.check(std::get<msgfmt::Error>(escape_error) == msgfmt::Error::invalid_utf8,
                  "escape UTF-8 error value");
}

void test_utf8_boundaries(TestContext& context)
{
    struct ValidCase {
        std::string_view name;
        std::string input;
        char32_t value;
        std::size_t utf16_units;
    };
    const std::vector<ValidCase> cases{
        {"NUL", bytes({0x00}), 0, 1},
        {"ASCII upper", bytes({0x7F}), 0x7F, 1},
        {"two-byte lower", bytes({0xC2, 0x80}), 0x80, 1},
        {"two-byte upper", bytes({0xDF, 0xBF}), 0x7FF, 1},
        {"three-byte lower", bytes({0xE0, 0xA0, 0x80}), 0x800, 1},
        {"before surrogates", bytes({0xED, 0x9F, 0xBF}), 0xD7FF, 1},
        {"after surrogates", bytes({0xEE, 0x80, 0x80}), 0xE000, 1},
        {"noncharacter FDD0", bytes({0xEF, 0xB7, 0x90}), 0xFDD0, 1},
        {"BOM", bytes({0xEF, 0xBB, 0xBF}), 0xFEFF, 1},
        {"noncharacter FFFE", bytes({0xEF, 0xBF, 0xBE}), 0xFFFE, 1},
        {"BMP upper", bytes({0xEF, 0xBF, 0xBF}), 0xFFFF, 1},
        {"four-byte lower", bytes({0xF0, 0x90, 0x80, 0x80}), 0x10000, 2},
        {"rocket", bytes({0xF0, 0x9F, 0x9A, 0x80}), 0x1F680, 2},
        {"F0 upper", bytes({0xF0, 0xBF, 0xBF, 0xBF}), 0x3FFFF, 2},
        {"F1 lower", bytes({0xF1, 0x80, 0x80, 0x80}), 0x40000, 2},
        {"F3 upper", bytes({0xF3, 0xBF, 0xBF, 0xBF}), 0xFFFFF, 2},
        {"F4 lower", bytes({0xF4, 0x80, 0x80, 0x80}), 0x100000, 2},
        {"Unicode upper", bytes({0xF4, 0x8F, 0xBF, 0xBF}), 0x10FFFF, 2},
    };
    for (const auto& item : cases) {
        const auto decoded = msgfmt::detail::decode_utf8(item.input, 0);
        context.check(decoded.has_value(), "accept boundary UTF-8", item.name);
        if (decoded) {
            context.check(decoded->value == item.value, "decode scalar value", item.name);
            context.check(decoded->byte_length == item.input.size(), "decode byte width", item.name);
            context.check(msgfmt::detail::utf16_width(decoded->value) == item.utf16_units,
                          "scalar UTF-16 width", item.name);
        }
        context.check(msgfmt::detail::utf16_length(item.input) == item.utf16_units,
                      "boundary UTF-16 length", item.name);

        const std::string padded = "A" + item.input + "B";
        const auto middle = msgfmt::detail::decode_utf8(padded, 1);
        context.check(middle && middle->value == item.value
                          && middle->byte_length == item.input.size(),
                      "decode at a nonzero byte offset", item.name);
        context.check(msgfmt::detail::utf16_length(padded) == item.utf16_units + 2,
                      "length of a mixed string", item.name);
        context.check(!msgfmt::detail::decode_utf8(item.input, item.input.size()),
                      "end offset has no code point", item.name);
        if (item.input.size() > 1) {
            context.check(!msgfmt::detail::decode_utf8(item.input, 1),
                          "continuation offset is invalid", item.name);
            const std::string_view truncated(item.input.data(), item.input.size() - 1);
            context.check(!msgfmt::detail::decode_utf8(truncated, 0),
                          "decoder respects view boundary", item.name);
        }
    }

    context.check(!msgfmt::detail::decode_utf8({}, 0), "empty view has no code point");
    context.check(msgfmt::detail::utf16_length({}) == 0, "empty UTF-16 length");
    context.check(!msgfmt::detail::decode_utf8("A", std::numeric_limits<std::size_t>::max()),
                  "large offset cannot overflow");
    context.check(msgfmt::detail::utf16_length(std::string("A\0B\r\n", 5)) == 5,
                  "NUL and CR/LF count as ordinary characters");
    context.check(msgfmt::detail::utf16_length(u8"\u0439") == 1, "composed character length");
    context.check(msgfmt::detail::utf16_length(u8"\u0438\u0306") == 2,
                  "combining sequence is not normalized");
    context.check(msgfmt::detail::utf16_length(u8"\U0001F469\u200D\U0001F4BB") == 5,
                  "ZWJ emoji counts UTF-16 units, not graphemes");

    for (const auto& item : invalid_inputs()) {
        context.check(!msgfmt::detail::decode_utf8(item.input, 0), "reject invalid UTF-8", item.name);
        context.check(!msgfmt::detail::utf16_length(item.input), "invalid string has no length",
                      item.name);
        const std::string padded = u8"\U0001F680 " + item.input + "X";
        context.check(!msgfmt::detail::utf16_length(padded), "validate after valid prefix", item.name);
    }
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

void test_all_scalars(TestContext& context)
{
    for (char32_t value = 0; value <= 0x10FFFF; ++value) {
        if (value >= 0xD800 && value <= 0xDFFF) {
            continue;
        }
        const auto input = encode_scalar(value);
        const auto decoded = msgfmt::detail::decode_utf8(input, 0);
        const bool correct = decoded && decoded->value == value
            && decoded->byte_length == input.size()
            && msgfmt::detail::utf16_length(input) == (value <= 0xFFFF ? 1U : 2U);
        context.check(correct, "round trip every Unicode scalar");
        if (!correct) {
            std::cerr << "Scalar: " << static_cast<unsigned int>(value) << '\n';
            return;
        }
    }
}

void test_two_byte_inputs(TestContext& context)
{
    for (unsigned int first = 0; first <= 0xFF; ++first) {
        const auto single = bytes({first});
        context.check(msgfmt::detail::decode_utf8(single, 0).has_value() == (first <= 0x7F),
                      "all single-byte inputs");
        for (unsigned int second = 0; second <= 0xFF; ++second) {
            const auto input = bytes({first, second});
            const bool ascii_pair = first <= 0x7F && second <= 0x7F;
            const bool encoded_pair = first >= 0xC2 && first <= 0xDF
                && second >= 0x80 && second <= 0xBF;
            context.check(msgfmt::detail::utf16_length(input).has_value()
                              == (ascii_pair || encoded_pair),
                          "all two-byte strings have correct validity");
        }
    }
}

void test_classification(TestContext& context)
{
    const std::array<char32_t, 14> letters{
        U'A', U'Z', U'a', U'z', 0x0400, 0x0401, 0x0410, 0x044F,
        0x0451, 0x0456, 0x0481, 0x048A, 0x0490, 0x04FF,
    };
    const std::array<char32_t, 18> nonletters{
        U'@', U'[', U'`', U'{', U'_', U'0', 0x03FF, 0x0482, 0x0483,
        0x0489, 0x0500, 0x00E9, 0x0306, 0x03B1, 0x4E2D, 0x1F680, 0, 0x10FFFF,
    };
    for (const auto value : letters) {
        context.check(msgfmt::detail::is_letter(value), "recognize agreed letters");
    }
    for (const auto value : nonletters) {
        context.check(!msgfmt::detail::is_letter(value), "exclude other characters from letters");
    }
    for (char32_t value = U'0'; value <= U'9'; ++value) {
        context.check(msgfmt::detail::is_digit(value), "ASCII digits");
    }
    for (const auto value : {U'/', U':', U'A', U'\u0660', U'\uFF10', U'\U0001F680'}) {
        context.check(!msgfmt::detail::is_digit(value), "exclude non-ASCII digits and symbols");
    }

    const std::array<char32_t, 25> whitespace{
        0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0, 0x1680,
        0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008,
        0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
    };
    for (const auto value : whitespace) {
        context.check(msgfmt::detail::is_whitespace(value), "all 25 White_Space characters");
        for (const auto neighbor : {value - 1, value + 1}) {
            const bool expected = std::find(whitespace.begin(), whitespace.end(), neighbor)
                != whitespace.end();
            context.check(msgfmt::detail::is_whitespace(neighbor) == expected,
                          "White_Space range boundaries");
        }
    }
    for (const auto value : {U'\0', U'A', U'\u180E', U'\u200B', U'\uFEFF', U'\U0001F680'}) {
        context.check(!msgfmt::detail::is_whitespace(value), "non-whitespace and BOM");
    }
}

void check_escape(TestContext& context, std::string_view input, std::string_view expected,
                  std::string_view description)
{
    const auto result = msgfmt::escape(input);
    const auto* output = std::get_if<std::string>(&result);
    context.check(output != nullptr, "escape accepts valid UTF-8", description);
    if (output) {
        context.check(*output == expected, "escape exact output bytes", description);
    }
}

void test_escape(TestContext& context)
{
    check_escape(context, {}, "", "empty default view");
    check_escape(context, "2 * 3", R"(2 \* 3)", "specification example");
    check_escape(context, "*_`[]()\\", R"(\*\_\`\[\]\(\)\\)", "all service characters");
    check_escape(context, R"(\*)", R"(\\\*)", "escape an existing escape");
    check_escape(context, R"(\q)", R"(\\q)", "escape every input backslash");
    check_escape(context, "\"/#<>+-={}!", "\"/#<>+-={}!", "other ASCII is unchanged");

    constexpr std::string_view services = "*_`[]()\\";
    for (unsigned int value = 0; value <= 0x7F; ++value) {
        const auto input = bytes({value});
        const auto expected = services.find(input.front()) == std::string_view::npos
            ? input : "\\" + input;
        check_escape(context, input, expected, "every ASCII byte including controls");
    }
    for (const std::size_t count : {1U, 2U, 3U, 16U, 4096U}) {
        check_escape(context, std::string(count, '\\'), std::string(2 * count, '\\'),
                     "consecutive backslashes");
        std::string expected;
        for (std::size_t i = 0; i < count; ++i) {
            expected += R"(\*)";
        }
        check_escape(context, std::string(count, '*'), expected, "consecutive markers");
    }

    const std::string unicode = u8"\uFEFF\u041F\u0440\u0438\u0432\u0435\u0442 "
        u8"\U0001F680 \u4E2D\u6587 \u0439 \u0438\u0306\r\n";
    check_escape(context, unicode, unicode, "preserve Unicode, BOM, normalization and CR/LF");
    check_escape(context, u8"\u00A0\u1680\u2000\u2028\u202F\u3000",
                 u8"\u00A0\u1680\u2000\u2028\u202F\u3000", "preserve Unicode whitespace");
    check_escape(context, std::string("A\0*\r\n", 5), std::string("A\0\\*\r\n", 6),
                 "NUL does not terminate the input");
    const std::string noncharacters = bytes({0xEF, 0xBF, 0xBE, 0xF4, 0x8F, 0xBF, 0xBF});
    check_escape(context, noncharacters, noncharacters, "valid noncharacters remain valid");

    std::string source = "*";
    const auto result = msgfmt::escape(source);
    source.assign("changed");
    const auto* owned = std::get_if<std::string>(&result);
    context.check(owned && *owned == R"(\*)", "escape result owns its bytes");
    const std::string longer = "*" + bytes({0xFF});
    check_escape(context, std::string_view(longer.data(), 1), R"(\*)",
                 "ignore bytes outside the input view");

    for (const auto& item : invalid_inputs()) {
        const std::array<std::string, 4> inputs{
            item.input, "*ok*" + item.input, item.input + "abc", "[x](" + item.input + ")",
        };
        for (const auto& input : inputs) {
            const auto escaped = msgfmt::escape(input);
            const auto* error = std::get_if<msgfmt::Error>(&escaped);
            context.check(error && *error == msgfmt::Error::invalid_utf8,
                          "escape returns only an error for invalid UTF-8", item.name);
        }
    }
}

struct ParseCase {
    std::string_view name;
    std::string input;
    std::string text;
    std::vector<msgfmt::Range> ranges;
};

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

void test_control_examples(TestContext& context)
{
    for (const auto& item : control_examples()) {
        check_parse(context, item);
    }
}

void test_parser_rules(TestContext& context)
{
    using msgfmt::Style;
    const std::vector<ParseCase> cases{
        {"plain bytes", "a\r\nb", "a\r\nb", {}},
        {"single stars", "*a*", "*a*", {}},
        {"nearest bold close", "**a**b**", "ab**", {{Style::bold, 0, 1, ""}}},
        {"adjacent bold", "**a****b**", "ab",
         {{Style::bold, 0, 1, ""}, {Style::bold, 1, 1, ""}}},
        {"triple stars are not another style", "***a***", "*a*", {{Style::bold, 0, 2, ""}}},
        {"unsuitable bold close is literal", "**a **b**", "a **b", {{Style::bold, 0, 5, ""}}},
        {"empty bold", "****", "****", {}},
        {"empty code", "``", "``", {}},
        {"empty italic", "__", "__", {}},
        {"ASCII letter boundary", "a_b_", "a_b_", {}},
        {"digit boundary", "0_b_", "0_b_", {}},
        {"Russian boundary", u8"я_б_!", u8"я_б_!", {}},
        {"Ukrainian boundary", u8"і_б_!", u8"і_б_!", {}},
        {"Latin classification limitation", u8"é_a_", u8"éa", {{Style::italic, 1, 1, ""}}},
        {"emoji boundary", u8"🚀_ок_", u8"🚀ок", {{Style::italic, 2, 2, ""}}},
        {"punctuation boundary", "(_b_)", "(b)", {{Style::italic, 1, 1, ""}}},
        {"italic cannot close before letter", "_a_b_", "a_b", {{Style::italic, 0, 3, ""}}},
        {"italic cannot close before digit", "_a_9", "_a_9", {}},
        {"italic cannot close before Cyrillic", u8"_a_я", u8"_a_я", {}},
        {"source boundary after markup", "**a**_b_", "ab",
         {{Style::bold, 0, 1, ""}, {Style::italic, 1, 1, ""}}},
        {"all escaped markers", R"(\*\_\`\[\]\(\)\\)", "*_`[]()\\", {}},
        {"backslash before ordinary character", R"(\q)", R"(\q)", {}},
        {"trailing backslash", "x\\", "x\\", {}},
        {"escaped closing marker", R"(**a\*\*b**)", "a**b", {{Style::bold, 0, 4, ""}}},
        {"raw unclosed style", R"(**a \*)", R"(**a \*)", {}},
        {"raw unclosed outer bold", "**a _b_", "**a _b_", {}},
        {"raw unclosed outer italic", "_a **b**", "_a **b**", {}},
        {"keep preceding completed range", "**ok** **bad _x_", "ok **bad _x_",
         {{Style::bold, 0, 2, ""}}},
        {"crossed markers stay raw", "**a _b** c_", "**a _b** c_", {}},
        {"ancestor marker is literal", "**a _b** c_ d**", "a b** c d",
         {{Style::bold, 0, 9, ""}, {Style::italic, 2, 5, ""}}},
        {"same style cannot open below top", "**a _b **c** d_ e**", "a b **c** d e",
         {{Style::bold, 0, 13, ""}, {Style::italic, 2, 9, ""}}},
        {"nested and subsequent ranges", "**a _b_ c** _d_", "a b c d",
         {{Style::bold, 0, 5, ""}, {Style::italic, 2, 1, ""}, {Style::italic, 6, 1, ""}}},
        {"same offset different lengths", "**_a_ b**", "a b",
         {{Style::bold, 0, 3, ""}, {Style::italic, 0, 1, ""}}},
        {"identical intervals bold first", "**_a_**", "a",
         {{Style::bold, 0, 1, ""}, {Style::italic, 0, 1, ""}}},
        {"identical intervals italic first", "_**a**_", "a",
         {{Style::italic, 0, 1, ""}, {Style::bold, 0, 1, ""}}},
        {"code suppresses markup and escapes", "`**a** _b_ [c](u) \\*`", "**a** _b_ [c](u) \\*",
         {{Style::code, 0, 19, ""}}},
        {"backslash cannot escape code closer", "`a\\`b", "a\\b", {{Style::code, 0, 2, ""}}},
        {"nearest code close", "`a`b`", "ab`", {{Style::code, 0, 1, ""}}},
        {"composed Unicode", u8"**й**", u8"й", {{Style::bold, 0, 1, ""}}},
        {"decomposed Unicode", u8"**и\u0306**", u8"и\u0306", {{Style::bold, 0, 2, ""}}},
        {"ZWJ emoji units", u8"**👩\u200D💻**", u8"👩\u200D💻", {{Style::bold, 0, 5, ""}}},
        {"BOM preserved", u8"\uFEFF**a**", u8"\uFEFFa", {{Style::bold, 1, 1, ""}}},
        {"NUL inside bold", std::string("**\0**", 5), std::string(1, '\0'),
         {{Style::bold, 0, 1, ""}}},
        {"NUL and CR/LF", std::string("A\0**b**\r\n", 9), std::string("A\0b\r\n", 5),
         {{Style::bold, 2, 1, ""}}},
        {"multiline bold", "**a\r\nb**", "a\r\nb", {{Style::bold, 0, 4, ""}}},
    };
    for (const auto& item : cases) {
        check_parse(context, item);
    }
}

void test_links(TestContext& context)
{
    using msgfmt::Style;
    const std::vector<ParseCase> cases{
        {"escaped label bracket", R"([a\]b](u))", "a]b", {{Style::link, 0, 3, "u"}}},
        {"escaped URL close", R"([x](a\)b))", "x", {{Style::link, 0, 1, "a)b"}}},
        {"escaped URL parentheses", "[x](a\\(b\\))", "x", {{Style::link, 0, 1, "a(b)"}}},
        {"escaped label underscore", R"([a\_b](u))", "a_b", {{Style::link, 0, 3, "u"}}},
        {"escaped URL underscore", R"([x](a\_b))", "x", {{Style::link, 0, 1, "a_b"}}},
        {"ordinary URL backslash", R"([x](a\qb))", "x", {{Style::link, 0, 1, R"(a\qb)"}}},
        {"escaped URL backslash", R"([x](a\\b))", "x", {{Style::link, 0, 1, R"(a\b)"}}},
        {"raw markup in URL", "[x](u_**[`)", "x", {{Style::link, 0, 1, "u_**[`"}}},
        {"no URL syntax validation", "[x](not-a-url)", "x", {{Style::link, 0, 1, "not-a-url"}}},
        {"URL parentheses not balanced", "[x](a(b)c)", "xc)", {{Style::link, 0, 1, "a(b"}}},
        {"following parentheses are text", "[x](u)(v)", "x(v)", {{Style::link, 0, 1, "u"}}},
        {"empty label", "[](u)", "[](u)", {}},
        {"empty URL", "[a]()", "[a]()", {}},
        {"missing label end", "[a", "[a", {}},
        {"missing URL opener", "[a]u", "[a]u", {}},
        {"missing URL end", "[a](u", "[a](u", {}},
        {"space between label and URL", "[a] (u)", "[a] (u)", {}},
        {"label trailing space", "[a ](u)", "[a ](u)", {}},
        {"URL space", "[x](a b)", "[x](a b)", {}},
        {"backslash does not escape URL space", "[x](a\\ b)", "[x](a\\ b)", {}},
        {"label ranges rolled back", "[**a**]()", "[**a**]()", {}},
        {"label escapes rolled back", R"([a\_b]())", R"([a\_b]())", {}},
        {"unclosed link is atomic", "[**a**](u **b**", "[**a**](u **b**", {}},
        {"link label bold", "[**a**](u)", "a",
         {{Style::link, 0, 1, "u"}, {Style::bold, 0, 1, ""}}},
        {"link inside bold", "**[a](u)**", "a",
         {{Style::bold, 0, 1, ""}, {Style::link, 0, 1, "u"}}},
        {"invalid link inside valid bold", "**[a]()**", "[a]()", {{Style::bold, 0, 5, ""}}},
        {"invalid supplementary link before bold", u8"[🚀]() **x**", u8"[🚀]() x",
         {{Style::bold, 7, 1, ""}}},
        {"resume after invalid label", "[a ](u) **b**", "[a ](u) b", {{Style::bold, 8, 1, ""}}},
        {"resume after invalid URL", "[a](u v) **b**", "[a](u v) b", {{Style::bold, 9, 1, ""}}},
        {"resume after missing URL opener", "[**a**]oops **b**", "[**a**]oops b",
         {{Style::bold, 12, 1, ""}}},
        {"same-style link opener is literal", "[a [b](v)](u)", "a [b](u)",
         {{Style::link, 0, 4, "v"}}},
        {"bare label end inside nested italic", "[_a]b_](u)", "[_a]b_](u)", {}},
        {"code protects label bracket", "[`a]b`](u)", "a]b",
         {{Style::link, 0, 3, "u"}, {Style::code, 0, 3, ""}}},
        {"URL NUL is ordinary", std::string("[x](a\0b)", 8), "x",
         {{Style::link, 0, 1, std::string("a\0b", 3)}}},
    };
    for (const auto& item : cases) {
        check_parse(context, item);
    }
}

void test_markup_whitespace(TestContext& context)
{
    const std::array<char32_t, 25> whitespace{
        0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0, 0x1680,
        0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008,
        0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
    };
    for (const auto value : whitespace) {
        const auto space = encode_scalar(value);
        for (const std::string_view marker : {"**", "_", "`"}) {
            const std::string opening = std::string(marker) + space + "a" + std::string(marker);
            const std::string closing = std::string(marker) + "a" + space + std::string(marker);
            check_parse(context, {"whitespace prevents opening", opening, opening, {}});
            check_parse(context, {"whitespace prevents closing", closing, closing, {}});
        }
        const auto label_start = "[" + space + "a](u)";
        const auto label_end = "[a" + space + "](u)";
        const auto url = "[a](u" + space + ")";
        check_parse(context, {"whitespace prevents link opening", label_start, label_start, {}});
        check_parse(context, {"whitespace prevents label closing", label_end, label_end, {}});
        check_parse(context, {"URL whitespace is invalid", url, url, {}});
        check_parse(context, {"whitespace still preserved in text", "x" + space + "**a**",
                              "x" + space + "a", {{msgfmt::Style::bold, 2, 1, ""}}});
    }
}

std::string wrap_style(msgfmt::Style style, const std::string& text)
{
    switch (style) {
    case msgfmt::Style::bold:
        return "**" + text + "**";
    case msgfmt::Style::italic:
        return "_" + text + "_";
    case msgfmt::Style::code:
        return "`" + text + "`";
    case msgfmt::Style::link:
        return "[" + text + "](u)";
    }
    return {};
}

void test_style_nesting(TestContext& context)
{
    using msgfmt::Style;
    const std::array<Style, 4> styles{Style::bold, Style::italic, Style::code, Style::link};
    for (const auto outer : styles) {
        if (outer == Style::code) {
            continue;
        }
        for (const auto inner : styles) {
            if (inner == outer) {
                continue;
            }
            const auto input = wrap_style(outer, wrap_style(inner, u8"🚀a"));
            check_parse(context, {"pairs of different styles", input, u8"🚀a",
                                  {{outer, 0, 3, outer == Style::link ? "u" : ""},
                                   {inner, 0, 3, inner == Style::link ? "u" : ""}}});
        }
    }
    std::array<Style, 3> outer_styles{Style::bold, Style::italic, Style::link};
    do {
        std::string input = u8"`🚀a`";
        for (auto style = outer_styles.rbegin(); style != outer_styles.rend(); ++style) {
            input = wrap_style(*style, input);
        }
        std::vector<msgfmt::Range> ranges;
        for (const auto style : outer_styles) {
            ranges.push_back({style, 0, 3, style == Style::link ? "u" : ""});
        }
        ranges.push_back({Style::code, 0, 3, ""});
        check_parse(context, {"all four styles", input, u8"🚀a", ranges});
    } while (std::next_permutation(outer_styles.begin(), outer_styles.end()));
}

void test_parse_errors_and_ownership(TestContext& context)
{
    for (const auto& item : invalid_inputs()) {
        const std::array<std::string, 8> inputs{
            item.input, "**ok**" + item.input, "**" + item.input + "**",
            "_" + item.input + "_", "`" + item.input + "`", "[x](" + item.input + ")",
            "[" + item.input + "](u)", "**bad _x_ " + item.input,
        };
        for (const auto& input : inputs) {
            const auto result = msgfmt::parse(input);
            const auto* error = std::get_if<msgfmt::Error>(&result);
            context.check(error && *error == msgfmt::Error::invalid_utf8,
                          "parse validates code, URL, literals and completed prefixes", item.name);
        }
    }
    check_parse(context, {"valid noncharacters", bytes({0xEF, 0xBF, 0xBF}),
                          bytes({0xEF, 0xBF, 0xBF}), {}});
    std::string source = "[**a**](u)";
    const auto result = msgfmt::parse(source);
    source.assign("changed");
    const auto* message = std::get_if<msgfmt::ParsedMessage>(&result);
    context.check(message && message->text == "a" && message->ranges.size() == 2
                      && message->ranges.front().url == "u", "parse owns text and URL");
    const std::string extended = "**a**" + bytes({0xFF});
    const auto bounded = msgfmt::parse(std::string_view(extended.data(), 5));
    const auto* bounded_message = std::get_if<msgfmt::ParsedMessage>(&bounded);
    context.check(bounded_message && bounded_message->text == "a"
                      && bounded_message->ranges.size() == 1, "parse respects view boundary");
    const auto empty = msgfmt::parse({});
    const auto* empty_message = std::get_if<msgfmt::ParsedMessage>(&empty);
    context.check(empty_message && empty_message->text.empty() && empty_message->ranges.empty(),
                  "parse empty default view");
}

void test_escape_parse_round_trip(TestContext& context)
{
    std::vector<std::string> inputs{
        "", "*_`[]()\\", "**a _b** c_", "[_a]b_](u)", R"([a\]b](a\)b))",
        std::string("a\0*\r\n", 5), u8"🚀 Привет, **мир**! и\u0306 \uFEFF",
        std::string(4096, '\\'), std::string(4096, '['),
    };
    std::string ascii;
    for (unsigned int value = 0; value <= 0x7F; ++value) {
        const auto byte = bytes({value});
        inputs.push_back(byte);
        ascii += byte;
    }
    inputs.push_back(ascii);
    for (const auto value : {char32_t{0x80}, char32_t{0x7FF}, char32_t{0x800}, char32_t{0xD7FF},
                             char32_t{0xE000}, char32_t{0xFFFF}, char32_t{0x10000},
                             char32_t{0x10FFFF}}) {
        inputs.push_back("**" + encode_scalar(value) + "_[]()`\\");
    }
    for (const auto& input : inputs) {
        const auto escaped = msgfmt::escape(input);
        const auto* text = std::get_if<std::string>(&escaped);
        context.check(text != nullptr, "round-trip input is valid UTF-8");
        if (text) {
            check_parse(context, {"parse(escape(x))", *text, input, {}});
        }
    }
}

class FailingInputBuffer : public std::streambuf {
public:
    explicit FailingInputBuffer(std::size_t prefix_size) : prefix_(prefix_size, 'a')
    {
        setg(prefix_.data(), prefix_.data(), prefix_.data() + prefix_.size());
    }

protected:
    int_type underflow() override
    {
        throw std::ios_base::failure("injected read failure");
    }

private:
    std::string prefix_;
};

class FailingOutputBuffer : public std::streambuf {
public:
    explicit FailingOutputBuffer(bool fail_on_flush) : fail_on_flush_(fail_on_flush) {}

protected:
    std::streamsize xsputn(const char*, std::streamsize count) override
    {
        return fail_on_flush_ ? count : 0;
    }

    int sync() override
    {
        return fail_on_flush_ ? -1 : 0;
    }

private:
    bool fail_on_flush_;
};

class GroupedPunctuation : public std::numpunct<char> {
protected:
    char do_thousands_sep() const override { return '_'; }
    std::string do_grouping() const override { return "\3"; }
};

void test_cli_streams(TestContext& context)
{
    for (const bool escape_mode : {false, true}) {
        for (const std::size_t prefix : {0U, 4096U}) {
            FailingInputBuffer source(prefix);
            std::istream input(&source);
            std::ostringstream output;
            std::ostringstream error;
            context.check(msgfmt::cli::run(input, output, error, escape_mode) == 1,
                          "CLI read failure returns 1");
            context.check(output.str().empty(), "read failure has no stdout prefix");
            context.check(error.str() == "msgfmt: failed to read stdin\n", "read error diagnostic");
        }
        for (const bool fail_on_flush : {false, true}) {
            std::istringstream input("**a**");
            FailingOutputBuffer sink(fail_on_flush);
            std::ostream output(&sink);
            std::ostringstream error;
            context.check(msgfmt::cli::run(input, output, error, escape_mode) == 1,
                          "CLI write/flush failure returns 1");
            context.check(error.str() == "msgfmt: failed to write stdout\n", "write error diagnostic");
        }
    }
    {
        std::istringstream input("unread");
        input.setstate(std::ios::failbit);
        std::ostringstream output;
        std::ostringstream error;
        context.check(msgfmt::cli::run(input, output, error, false) == 1,
                      "failed stream without EOF is not an empty success");
        context.check(output.str().empty(), "failed stream has empty stdout");
    }
    {
        std::istringstream input("");
        FailingOutputBuffer sink(true);
        std::ostream output(&sink);
        std::ostringstream error;
        context.check(msgfmt::cli::run(input, output, error, true) == 1,
                      "empty escape output still checks flush");
    }
    {
        std::istringstream input(bytes({0xFF}));
        FailingOutputBuffer sink(false);
        std::ostream output(&sink);
        std::ostringstream error;
        context.check(msgfmt::cli::run(input, output, error, false) == 1,
                      "failed error-object output is an I/O error");
    }
    {
        const auto previous_locale = std::locale::global(
            std::locale(std::locale::classic(), new GroupedPunctuation));
        std::istringstream input(std::string(1000, 'a') + "**b**");
        std::ostringstream output;
        std::ostringstream error;
        const int status = msgfmt::cli::run(input, output, error, false);
        std::locale::global(previous_locale);
        const auto expected = "{\"text\":\"" + std::string(1000, 'a')
            + "b\",\"ranges\":[{\"style\":\"bold\",\"offset\":1000,\"length\":1,\"url\":\"\"}]}";
        context.check(status == 0 && output.str() == expected && error.str().empty(),
                      "JSON numbers are independent of the global locale");
    }
}

void write_fixture(const std::filesystem::path& path, std::string_view data)
{
    std::ofstream file(path, std::ios::binary);
    if (!data.empty()) {
        file.write(data.data(), static_cast<std::streamsize>(data.size()));
    }
    file.close();
    if (!file) {
        throw std::runtime_error("could not write CLI fixture: " + path.string());
    }
}

void write_cli_fixtures(const std::filesystem::path& directory)
{
    std::filesystem::create_directories(directory);
    std::string manifest;
    const auto add = [&directory, &manifest](const std::string& name, std::string_view input,
                                           std::string_view output, int status = 0,
                                           std::string_view error = {}) {
        write_fixture(directory / (name + ".in"), input);
        write_fixture(directory / (name + ".out"), output);
        write_fixture(directory / (name + ".err"), error);
        write_fixture(directory / (name + ".status"), std::to_string(status));
        manifest += name + '\n';
    };

    // Explicit expected JSON, independent of the production serializer.
    const std::array<std::string, 15> expected_control{
        u8R"({"text":"важно","ranges":[{"style":"bold","offset":0,"length":5,"url":""}]})",
        u8R"({"text":"Привет, мир!","ranges":[{"style":"bold","offset":8,"length":3,"url":""}]})",
        u8R"({"text":"курсив","ranges":[{"style":"italic","offset":0,"length":6,"url":""}]})",
        R"({"text":"a_b_c","ranges":[]})",
        u8R"({"text":"**не закрыто","ranges":[]})",
        R"({"text":"2 * 3","ranges":[]})",
        u8R"({"text":"чат * список","ranges":[{"style":"code","offset":0,"length":12,"url":""}]})",
        u8R"({"text":"документация","ranges":[{"style":"link","offset":0,"length":12,"url":"https://docs.github.com"}]})",
        u8R"({"text":"жирный и курсив","ranges":[{"style":"bold","offset":0,"length":15,"url":""},{"style":"italic","offset":7,"length":8,"url":""}]})",
        u8R"({"text":"🚀 ок","ranges":[{"style":"bold","offset":3,"length":2,"url":""}]})",
        u8R"({"text":"🚀 ок","ranges":[{"style":"bold","offset":0,"length":5,"url":""}]})",
        u8R"json({"text":"[текст]()","ranges":[]})json",
        R"({"text":"\\","ranges":[]})",
        R"({"text":"","ranges":[]})",
        R"({"text":"_ e_","ranges":[]})",
    };
    const auto controls = control_examples();
    if (controls.size() != expected_control.size()) {
        throw std::runtime_error("CLI control inputs and expected JSON have different counts");
    }
    for (std::size_t i = 0; i < controls.size(); ++i) {
        add("parse_A" + std::to_string(i + 1), controls[i].input, expected_control[i]);
    }

    std::string control_bytes;
    for (unsigned int value = 0; value < 0x20; ++value) {
        control_bytes.push_back(static_cast<char>(value));
    }
    add("parse_json_controls", control_bytes + "\"\\/",
        R"({"text":"\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\b\t\n\u000b\f\r\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f\"\\/","ranges":[]})");
    add("parse_url_json", R"([x](a"\b))",
        R"({"text":"x","ranges":[{"style":"link","offset":0,"length":1,"url":"a\"\\b"}]})");
    add("parse_url_nul", std::string("[x](a\0b)", 8),
        R"({"text":"x","ranges":[{"style":"link","offset":0,"length":1,"url":"a\u0000b"}]})");
    add("parse_unicode_crlf", u8"\uFEFF🚀 **и\u0306**\r\n",
        u8"{\"text\":\"\uFEFF🚀 и\u0306\\r\\n\",\"ranges\":[{\"style\":\"bold\",\"offset\":4,\"length\":2,\"url\":\"\"}]}");
    add("parse_read_boundary", std::string(4096, 'a'),
        "{\"text\":\"" + std::string(4096, 'a') + "\",\"ranges\":[]}");
    add("parse_large_offset", std::string(10000, 'a') + "**b**",
        "{\"text\":\"" + std::string(10000, 'a')
            + "b\",\"ranges\":[{\"style\":\"bold\",\"offset\":10000,\"length\":1,\"url\":\"\"}]}");
    add("parse_equal_intervals", "[**a**](u)",
        R"({"text":"a","ranges":[{"style":"link","offset":0,"length":1,"url":"u"},{"style":"bold","offset":0,"length":1,"url":""}]})");

    add("escape_empty", "", "");
    add("escape_example", "2 * 3", R"(2 \* 3)");
    add("escape_service", "*_`[]()\\", R"(\*\_\`\[\]\(\)\\)");
    add("escape_existing", R"(\*)", R"(\\\*)");
    add("escape_controls", control_bytes + "\"\\/", control_bytes + "\"\\\\/");
    const std::string unicode = u8"\uFEFF🚀 Привет, и\u0306!\r\n";
    add("escape_unicode_crlf", unicode, unicode);
    std::string escaped_stars;
    for (std::size_t i = 0; i < 8192; ++i) {
        escaped_stars += R"(\*)";
    }
    add("escape_large", std::string(8192, '*'), escaped_stars);

    constexpr std::string_view invalid_json = R"({"error":"invalid_utf8"})";
    std::size_t invalid_index = 0;
    for (const auto& item : invalid_inputs()) {
        if (item.name == "isolated continuation 80" || item.name == "overlong E0 lower"
            || item.name == "first surrogate" || item.name == "U+110000"
            || item.name == "three-byte bad third" || item.name == "truncated four-byte 3") {
            const auto suffix = std::to_string(invalid_index++);
            add("parse_invalid_" + suffix, item.input, invalid_json, 2);
            add("escape_invalid_" + suffix, item.input, invalid_json, 2);
        }
    }
    if (invalid_index != 6) {
        throw std::runtime_error("CLI invalid-UTF-8 fixtures are incomplete");
    }
    const auto invalid = bytes({0xFF});
    add("parse_invalid_code", "`" + invalid + "`", invalid_json, 2);
    add("parse_invalid_url", "[a](" + invalid + ")", invalid_json, 2);
    add("parse_invalid_unclosed", "**a " + invalid, invalid_json, 2);
    add("parse_invalid_prefix", "**ok**" + invalid, invalid_json, 2);
    add("parse_invalid_after_chunks", std::string(8192, 'a') + invalid, invalid_json, 2);
    add("escape_invalid_after_chunks", std::string(8192, 'a') + invalid, invalid_json, 2);

    constexpr std::string_view usage = "Usage: msgfmt [escape]\n";
    add("args_unknown", "**a**", "", 1, usage);
    add("args_help", "", "", 1, usage);
    add("args_extra", invalid, "", 1, usage);
    add("args_case", "", "", 1, usage);
    write_fixture(directory / "cases.txt", manifest);
}

} // namespace

int main(int argc, char* argv[])
{
    if (argc == 3 && std::string_view(argv[1]) == "write_cli_fixtures") {
        try {
            write_cli_fixtures(argv[2]);
            return 0;
        } catch (const std::exception& error) {
            std::cerr << error.what() << '\n';
            return 1;
        }
    }
    const std::string_view group = argc == 1 ? "all" : argv[1];
    if (argc > 2 || (group != "all" && group != "public_api" && group != "utf8"
                     && group != "escape" && group != "parser" && group != "cli_streams")) {
        std::cerr << "Usage: msgfmt_tests [all|public_api|utf8|escape|parser|cli_streams]\n";
        return 1;
    }

    TestContext context;
    if (group == "all" || group == "public_api") {
        test_public_api(context);
    }
    if (group == "all" || group == "utf8") {
        test_utf8_boundaries(context);
        test_all_scalars(context);
        test_two_byte_inputs(context);
        test_classification(context);
    }
    if (group == "all" || group == "escape") {
        test_escape(context);
    }
    if (group == "all" || group == "parser") {
        test_control_examples(context);
        test_parser_rules(context);
        test_links(context);
        test_markup_whitespace(context);
        test_style_nesting(context);
        test_parse_errors_and_ownership(context);
        test_escape_parse_round_trip(context);
    }
    if (group == "all" || group == "cli_streams") {
        test_cli_streams(context);
    }
    if (context.failures != 0) {
        std::cerr << context.failures << " of " << context.checks << " checks failed.\n";
        return 1;
    }
    std::cout << group << ": " << context.checks << " checks passed.\n";
    return 0;
}
