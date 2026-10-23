#pragma once

#include "msgfmt.hpp"

#include <array>
#include <filesystem>
#include <initializer_list>
#include <iostream>
#include <string>
#include <string_view>
#include <vector>

namespace msgfmt::tests {

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

std::string bytes(std::initializer_list<unsigned int> values);
std::string encode_scalar(char32_t value);

struct InvalidCase {
    std::string_view name;
    std::string input;
};

std::vector<InvalidCase> invalid_inputs();
std::vector<InvalidCase> representative_invalid_inputs();

inline constexpr std::array<char32_t, 25> whitespace{
    0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0020, 0x0085, 0x00A0, 0x1680,
    0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008,
    0x2009, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
};

struct ParseCase {
    std::string_view name;
    std::string input;
    std::string text;
    std::vector<msgfmt::Range> ranges;
};

std::vector<ParseCase> control_examples();
void check_parse(TestContext& context, const ParseCase& item);

void run_utf8_tests(TestContext& context);
void run_escape_tests(TestContext& context);
void run_parser_tests(TestContext& context);
void run_cli_stream_tests(TestContext& context);
void write_cli_fixtures(const std::filesystem::path& directory);

} // namespace msgfmt::tests
