#include "test_support.hpp"

#include <array>
#include <fstream>
#include <locale>
#include <sstream>
#include <stdexcept>
#include <streambuf>

// Private CLI entry point from main.cpp, linked without its process main.
namespace msgfmt::cli {
int run(std::istream& input, std::ostream& output, std::ostream& error, bool escape_mode);
}

namespace msgfmt::tests {
namespace {

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

} // namespace

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
    for (const auto& item : representative_invalid_inputs()) {
        const auto suffix = std::to_string(invalid_index++);
        add("parse_invalid_" + suffix, item.input, invalid_json, 2);
        add("escape_invalid_" + suffix, item.input, invalid_json, 2);
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

void run_cli_stream_tests(TestContext& context)
{
    test_cli_streams(context);
}

} // namespace msgfmt::tests
