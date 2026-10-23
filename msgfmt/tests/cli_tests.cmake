cmake_minimum_required(VERSION 3.20)

if(NOT DEFINED MSGFMT_EXECUTABLE)
    message(FATAL_ERROR "MSGFMT_EXECUTABLE is required")
endif()

# Stage 4 replaces this skeleton check with the actual CLI contract tests.
execute_process(
    COMMAND "${MSGFMT_EXECUTABLE}"
    RESULT_VARIABLE cli_result
    OUTPUT_VARIABLE cli_output
    ERROR_VARIABLE cli_error
    TIMEOUT 5
)

if(NOT cli_result STREQUAL "1")
    message(FATAL_ERROR "Skeleton CLI must return 1; got: ${cli_result}; stderr: ${cli_error}")
endif()
if(NOT cli_output STREQUAL "")
    message(FATAL_ERROR "Skeleton CLI must not produce a successful-looking stdout result")
endif()
if(NOT cli_error MATCHES "CLI is not implemented yet")
    message(FATAL_ERROR "Skeleton CLI must report its implementation status; stderr: ${cli_error}")
endif()
