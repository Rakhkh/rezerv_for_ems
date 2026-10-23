#include "msgfmt.hpp"

#include "utf8.hpp"

#include <algorithm>
#include <utility>

namespace msgfmt {
namespace {

bool is_service_character(char32_t value) noexcept
{
    switch (value) {
    case U'*':
    case U'_':
    case U'`':
    case U'[':
    case U']':
    case U'(':
    case U')':
    case U'\\':
        return true;
    default:
        return false;
    }
}

bool is_word_character(char32_t value) noexcept
{
    return detail::is_letter(value) || detail::is_digit(value);
}

struct OpenStyle {
    Style style;
    std::size_t source_byte_begin;
    std::size_t source_utf16_begin;
    std::size_t text_byte_begin;
    std::size_t text_utf16_begin;
    std::size_t ranges_begin;
    std::size_t order;
    bool reading_url = false;
    bool valid = true;
    std::string url;
};

struct OrderedRange {
    Range range;
    std::size_t order;
};

class Parser {
public:
    explicit Parser(std::string_view input) : input_(input)
    {
        text_.reserve(input.size());
    }

    ParseResult run()
    {
        while (position_ < input_.size()) {
            const auto current = detail::decode_utf8(input_, position_);
            if (!current) {
                return Error::invalid_utf8;
            }

            if (!stack_.empty() && stack_.back().style == Style::code) {
                if (current->value == U'`' && can_close(Style::code, 1)) {
                    consume_ascii("`");
                    finish_top(true);
                } else {
                    append_code_point(*current);
                }
                continue;
            }

            // Escaped delimiters are ordinary content, never parsed a second time.
            if (current->value == U'\\') {
                const auto next = detail::decode_utf8(input_, position_ + 1);
                if (next && is_service_character(next->value)) {
                    consume(*current);
                    append_code_point(*next);
                    continue;
                }
            }

            if (reading_url()) {
                if (current->value == U')') {
                    consume_ascii(")");
                    const bool valid = stack_.back().valid && !stack_.back().url.empty();
                    finish_top(valid);
                } else {
                    if (detail::is_whitespace(current->value)) {
                        stack_.back().valid = false;
                    }
                    append_code_point(*current);
                }
                continue;
            }

            if (has("**")) {
                handle_marker(Style::bold, "**");
            } else if (current->value == U'_') {
                handle_marker(Style::italic, "_");
            } else if (current->value == U'`') {
                handle_marker(Style::code, "`");
            } else if (current->value == U'[') {
                handle_marker(Style::link, "[");
            } else if (current->value == U']') {
                handle_label_end(*current);
            } else {
                append_code_point(*current);
            }
        }

        if (!stack_.empty()) {
            // All unfinished descendants belong to the earliest unfinished style.
            const auto unfinished = std::move(stack_.front());
            stack_.clear();
            restore_literal(unfinished);
        }

        std::sort(ranges_.begin(), ranges_.end(), [](const auto& left, const auto& right) {
            if (left.range.offset != right.range.offset) {
                return left.range.offset < right.range.offset;
            }
            if (left.range.length != right.range.length) {
                return left.range.length > right.range.length;
            }
            return left.order < right.order;
        });

        ParsedMessage message{std::move(text_), {}};
        message.ranges.reserve(ranges_.size());
        for (auto& entry : ranges_) {
            message.ranges.push_back(std::move(entry.range));
        }
        return message;
    }

private:
    bool has(std::string_view marker) const noexcept
    {
        return input_.substr(position_, marker.size()) == marker;
    }

    bool reading_url() const noexcept
    {
        return !stack_.empty() && stack_.back().reading_url;
    }

    void consume(const detail::DecodedCodePoint& point) noexcept
    {
        position_ += point.byte_length;
        source_utf16_ += detail::utf16_width(point.value);
        previous_ = point.value;
    }

    // Only called for ASCII markers already matched at the current position.
    void consume_ascii(std::string_view marker) noexcept
    {
        position_ += marker.size();
        source_utf16_ += marker.size();
        previous_ = static_cast<unsigned char>(marker.back());
    }

    void append_code_point(const detail::DecodedCodePoint& point)
    {
        const auto source = input_.substr(position_, point.byte_length);
        if (reading_url()) {
            stack_.back().url.append(source);
        } else {
            text_.append(source);
            text_utf16_ += detail::utf16_width(point.value);
        }
        consume(point);
    }

    bool is_open(Style style) const noexcept
    {
        return std::any_of(stack_.begin(), stack_.end(), [style](const auto& frame) {
            return frame.style == style;
        });
    }

    bool can_open(Style style, std::size_t marker_length) const noexcept
    {
        const auto next = detail::decode_utf8(input_, position_ + marker_length);
        return next && !detail::is_whitespace(next->value)
            && (style != Style::italic || !is_word_character(previous_));
    }

    bool can_close(Style style, std::size_t marker_length) const noexcept
    {
        if (stack_.empty() || stack_.back().style != style
            || text_utf16_ == stack_.back().text_utf16_begin
            || detail::is_whitespace(previous_)) {
            return false;
        }
        if (style == Style::italic) {
            const auto next = detail::decode_utf8(input_, position_ + marker_length);
            return position_ + marker_length == input_.size()
                || (next && !is_word_character(next->value));
        }
        return true;
    }

    void handle_marker(Style style, std::string_view marker)
    {
        if (style != Style::link && can_close(style, marker.size())) {
            consume_ascii(marker);
            finish_top(true);
        } else if (!is_open(style) && can_open(style, marker.size())) {
            stack_.push_back(OpenStyle{style, position_, source_utf16_, text_.size(),
                                      text_utf16_, ranges_.size(), next_order_++, false, true, {}});
            consume_ascii(marker);
        } else {
            text_.append(marker);
            text_utf16_ += marker.size();
            consume_ascii(marker);
        }
    }

    void handle_label_end(const detail::DecodedCodePoint& point)
    {
        if (!stack_.empty() && stack_.back().style == Style::link) {
            auto& link = stack_.back();
            link.valid = link.valid && text_utf16_ > link.text_utf16_begin
                && !detail::is_whitespace(previous_);
            if (has("](")) {
                consume_ascii("](");
                link.reading_url = true;
            } else {
                consume_ascii("]");
                finish_top(false);
            }
        } else {
            // A bare ']' inside an unfinished non-code style cannot close the
            // ancestor link, and is not permitted as unescaped label content.
            for (auto& frame : stack_) {
                if (frame.style == Style::link) {
                    frame.valid = false;
                }
            }
            append_code_point(point);
        }
    }

    void restore_literal(const OpenStyle& frame)
    {
        text_.resize(frame.text_byte_begin);
        text_.append(input_.substr(frame.source_byte_begin, position_ - frame.source_byte_begin));
        text_utf16_ = frame.text_utf16_begin + (source_utf16_ - frame.source_utf16_begin);
        ranges_.resize(frame.ranges_begin);
    }

    void finish_top(bool valid)
    {
        auto frame = std::move(stack_.back());
        stack_.pop_back();
        if (valid) {
            ranges_.push_back(OrderedRange{
                {frame.style, frame.text_utf16_begin, text_utf16_ - frame.text_utf16_begin,
                 std::move(frame.url)},
                frame.order,
            });
        } else {
            restore_literal(frame);
        }
    }

    std::string_view input_;
    std::size_t position_ = 0;
    std::size_t source_utf16_ = 0;
    std::size_t text_utf16_ = 0;
    std::size_t next_order_ = 0;
    // Zero has the same non-letter/non-whitespace boundary behavior as BOF.
    char32_t previous_ = 0;
    std::string text_;
    std::vector<OpenStyle> stack_;
    std::vector<OrderedRange> ranges_;
};

} // namespace

EscapeResult escape(std::string_view input)
{
    std::string output;
    output.reserve(input.size());
    std::size_t offset = 0;
    while (offset < input.size()) {
        const auto decoded = detail::decode_utf8(input, offset);
        if (!decoded) {
            return Error::invalid_utf8;
        }
        if (is_service_character(decoded->value)) {
            output.push_back('\\');
        }
        output.append(input.substr(offset, decoded->byte_length));
        offset += decoded->byte_length;
    }
    return output;
}

ParseResult parse(std::string_view input)
{
    return Parser(input).run();
}

} // namespace msgfmt
