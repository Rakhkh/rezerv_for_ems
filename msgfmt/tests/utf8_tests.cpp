#include "test_support.hpp"
#include "utf8.hpp"

#include <algorithm>
#include <array>
#include <limits>

namespace msgfmt::tests {
namespace {

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

} // namespace

void run_utf8_tests(TestContext& context)
{
    test_utf8_boundaries(context);
    test_all_scalars(context);
    test_two_byte_inputs(context);
    test_classification(context);
}

} // namespace msgfmt::tests
