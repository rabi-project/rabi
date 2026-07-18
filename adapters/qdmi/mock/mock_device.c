/* SPDX-License-Identifier: Apache-2.0
 *
 * Mock QDMI device: a minimal, self-contained implementation of the QDMI
 * 1.0-shaped device interface rabi_qdmi binds (see device.py SYMBOLS —
 * one table on each side keeps a real device's ABI drift a one-file fix).
 * Jobs "execute" instantly with an ideal Bell histogram; cancellation of a
 * queued job wins. Built by tests/CI with a plain C compiler.
 */
#include <stdlib.h>
#include <string.h>

#define QDMI_SUCCESS 0
#define QDMI_ERROR_INVALIDARGUMENT 1
#define QDMI_ERROR_NOTFOUND 2

/* status codes mirrored in device.py */
#define QDMI_JOB_QUEUED 0
#define QDMI_JOB_RUNNING 1
#define QDMI_JOB_DONE 2
#define QDMI_JOB_FAILED 3
#define QDMI_JOB_CANCELLED 4

typedef struct {
  int status;
  long shots;
} mock_job;

static const char device_name[] = "mock-qdmi-device";
static const char device_version[] = "mock-1.0.0";

int QDMI_device_initialize(void) { return QDMI_SUCCESS; }
int QDMI_device_finalize(void) { return QDMI_SUCCESS; }

int QDMI_device_query_name(char *buf, size_t size) {
  if (!buf || size < sizeof(device_name)) return QDMI_ERROR_INVALIDARGUMENT;
  memcpy(buf, device_name, sizeof(device_name));
  return QDMI_SUCCESS;
}

int QDMI_device_query_version(char *buf, size_t size) {
  if (!buf || size < sizeof(device_version)) return QDMI_ERROR_INVALIDARGUMENT;
  memcpy(buf, device_version, sizeof(device_version));
  return QDMI_SUCCESS;
}

int QDMI_device_query_qubits_num(int *num) {
  if (!num) return QDMI_ERROR_INVALIDARGUMENT;
  *num = 7;
  return QDMI_SUCCESS;
}

/* Two-qubit gate error for a coupler; a small deterministic gradient. */
int QDMI_device_query_operation_error(const char *op, int q0, int q1, double *err) {
  if (!op || !err || q0 < 0 || q1 < 0 || q0 > 6 || q1 > 6)
    return QDMI_ERROR_INVALIDARGUMENT;
  if (strcmp(op, "cz") != 0) return QDMI_ERROR_NOTFOUND;
  *err = 0.004 + 0.001 * (double)(q0 + q1);
  return QDMI_SUCCESS;
}

int QDMI_device_query_readout_error(int qubit, double *err) {
  if (!err || qubit < 0 || qubit > 6) return QDMI_ERROR_INVALIDARGUMENT;
  *err = 0.015 + 0.002 * (double)qubit;
  return QDMI_SUCCESS;
}

int QDMI_device_job_submit(const char *program, long shots, void **job_out) {
  if (!program || shots <= 0 || !job_out) return QDMI_ERROR_INVALIDARGUMENT;
  if (strstr(program, "OPENQASM") == NULL) return QDMI_ERROR_INVALIDARGUMENT;
  mock_job *job = (mock_job *)malloc(sizeof(mock_job));
  if (!job) return QDMI_ERROR_INVALIDARGUMENT;
  job->status = QDMI_JOB_DONE; /* instant execution */
  job->shots = shots;
  *job_out = job;
  return QDMI_SUCCESS;
}

int QDMI_device_job_status(void *job_ptr, int *status) {
  if (!job_ptr || !status) return QDMI_ERROR_INVALIDARGUMENT;
  *status = ((mock_job *)job_ptr)->status;
  return QDMI_SUCCESS;
}

int QDMI_device_job_cancel(void *job_ptr) {
  mock_job *job = (mock_job *)job_ptr;
  if (!job) return QDMI_ERROR_INVALIDARGUMENT;
  if (job->status == QDMI_JOB_QUEUED || job->status == QDMI_JOB_RUNNING)
    job->status = QDMI_JOB_CANCELLED;
  return QDMI_SUCCESS;
}

/* Bell histogram over "00"/"11". Caller provides parallel arrays. */
int QDMI_device_job_result_hist(void *job_ptr, char keys[][8], long *values,
                                size_t capacity, size_t *entries) {
  mock_job *job = (mock_job *)job_ptr;
  if (!job || !keys || !values || !entries || capacity < 2)
    return QDMI_ERROR_INVALIDARGUMENT;
  if (job->status != QDMI_JOB_DONE) return QDMI_ERROR_NOTFOUND;
  strcpy(keys[0], "00");
  strcpy(keys[1], "11");
  values[0] = job->shots / 2;
  values[1] = job->shots - job->shots / 2;
  *entries = 2;
  return QDMI_SUCCESS;
}

int QDMI_device_job_free(void *job_ptr) {
  free(job_ptr);
  return QDMI_SUCCESS;
}
