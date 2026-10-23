cmake_minimum_required(VERSION 3.20)

foreach(required MSGFMT_EXECUTABLE MSGFMT_TEST_EXECUTABLE MSGFMT_FIXTURE_DIR)
    if(NOT DEFINED ${required})
        message(FATAL_ERROR "${required} is required")
    endif()
endforeach()

execute_process(
    COMMAND "${MSGFMT_TEST_EXECUTABLE}" write_cli_fixtures "${MSGFMT_FIXTURE_DIR}"
    RESULT_VARIABLE fixture_result
    OUTPUT_VARIABLE fixture_output
    ERROR_VARIABLE fixture_error
    TIMEOUT 10
)
if(NOT fixture_result STREQUAL "0")
    message(FATAL_ERROR "Fixture generation failed: ${fixture_result}; ${fixture_error}")
endif()

# Read a manifest rather than globbing: files left by older runs are not tests.
file(STRINGS "${MSGFMT_FIXTURE_DIR}/cases.txt" cases)
if(NOT cases)
    message(FATAL_ERROR "No CLI cases were generated")
endif()

foreach(case IN LISTS cases)
    set(arguments)
    if(case MATCHES "^escape_")
        set(arguments escape)
    elseif(case STREQUAL "args_unknown")
        set(arguments unknown)
    elseif(case STREQUAL "args_help")
        set(arguments --help)
    elseif(case STREQUAL "args_extra")
        set(arguments escape extra)
    elseif(case STREQUAL "args_case")
        set(arguments Escape)
    elseif(NOT case MATCHES "^parse_")
        message(FATAL_ERROR "Unknown CLI case: ${case}")
    endif()

    set(prefix "${MSGFMT_FIXTURE_DIR}/${case}")
    execute_process(
        COMMAND "${MSGFMT_EXECUTABLE}" ${arguments}
        INPUT_FILE "${prefix}.in"
        OUTPUT_FILE "${prefix}.actual.out"
        ERROR_FILE "${prefix}.actual.err"
        RESULT_VARIABLE actual_status
        TIMEOUT 5
    )
    file(READ "${prefix}.status" expected_status)
    if(NOT actual_status STREQUAL expected_status)
        message(FATAL_ERROR
            "${case}: expected exit ${expected_status}, got ${actual_status}. "
            "See ${prefix}.actual.err")
    endif()

    # HEX makes comparison binary-safe for NUL, Ctrl-Z and CR/LF. Expected JSON
    # is explicit fixture data, not output from the library or its serializer.
    foreach(stream out err)
        file(READ "${prefix}.${stream}" expected HEX)
        file(READ "${prefix}.actual.${stream}" actual HEX)
        if(NOT actual STREQUAL expected)
            message(FATAL_ERROR
                "${case}: ${stream} bytes differ; compare ${prefix}.${stream} "
                "and ${prefix}.actual.${stream}")
        endif()
    endforeach()
endforeach()

list(LENGTH cases case_count)
message(STATUS "${case_count} CLI cases passed (exit status, stdout and stderr bytes)")
