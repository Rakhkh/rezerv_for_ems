#include "msgfmt.hpp"
#include "utf8.hpp"

#include <algorithm>
#include <array>
#include <initializer_list>
#include <iostream>
#include <limits>
#include <string>
#include <string_view>
#include <type_traits>
#include <utility>
#include <variant>
#include <vector>

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

} // namespace

int main(int argc, char* argv[])
{
    const std::string_view group = argc == 1 ? "all" : argv[1];
    if (argc > 2 || (group != "all" && group != "public_api" && group != "utf8"
                     && group != "escape")) {
        std::cerr << "Usage: msgfmt_tests [all|public_api|utf8|escape]\n";
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
    if (context.failures != 0) {
        std::cerr << context.failures << " of " << context.checks << " checks failed.\n";
        return 1;
    }
    std::cout << group << ": " << context.checks << " checks passed.\n";
    return 0;
}
