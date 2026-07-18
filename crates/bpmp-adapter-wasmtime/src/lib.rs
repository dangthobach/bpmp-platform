//! Sandboxed in-process execution for versioned BPMP WebAssembly workers.

use std::ops::Range;

use anyhow::Error;
use bpmp_domain_core::LocalWasmPolicy;
use thiserror::Error;
use wasmtime::{Config, Engine, Module, ResourceLimiter, Store, Trap};

/// Immutable host/guest protocol version. Changes require a new ABI contract.
pub const BPMP_WASM_ABI_VERSION: i32 = 1;

const ABI_VERSION_EXPORT: &str = "bpmp_abi_version";
const ALLOC_EXPORT: &str = "bpmp_alloc";
const RUN_EXPORT: &str = "bpmp_run";
const MEMORY_EXPORT: &str = "memory";

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct WasmWorkerConfig {
    pub max_wasm_stack_bytes: usize,
}

impl WasmWorkerConfig {
    /// Validates process-level Wasmtime configuration.
    ///
    /// # Errors
    ///
    /// Returns [`WasmConfigError`] when a required bound is zero.
    pub fn validate(&self) -> Result<(), WasmConfigError> {
        positive(self.max_wasm_stack_bytes, "max_wasm_stack_bytes")
    }
}

#[derive(Debug, Clone, Eq, PartialEq)]
pub struct WasmExecutionLimits {
    pub max_module_bytes: usize,
    pub max_input_bytes: usize,
    pub max_output_bytes: usize,
    pub max_memory_bytes: usize,
    pub max_table_elements: usize,
    pub max_instances: usize,
    pub max_tables: usize,
    pub max_memories: usize,
    pub fuel: u64,
}

impl WasmExecutionLimits {
    /// Validates all per-execution resource bounds.
    ///
    /// # Errors
    ///
    /// Returns [`WasmConfigError`] for zero values or ABI-incompatible payload bounds.
    pub fn validate(&self) -> Result<(), WasmConfigError> {
        positive(self.max_module_bytes, "max_module_bytes")?;
        positive(self.max_input_bytes, "max_input_bytes")?;
        positive(self.max_output_bytes, "max_output_bytes")?;
        positive(self.max_memory_bytes, "max_memory_bytes")?;
        positive(self.max_table_elements, "max_table_elements")?;
        positive(self.max_instances, "max_instances")?;
        positive(self.max_tables, "max_tables")?;
        positive(self.max_memories, "max_memories")?;
        if self.fuel == 0 {
            return Err(WasmConfigError::NonPositive("fuel"));
        }
        for (field, value) in [
            ("max_input_bytes", self.max_input_bytes),
            ("max_output_bytes", self.max_output_bytes),
        ] {
            if value > i32::MAX as usize {
                return Err(WasmConfigError::AbiLengthExceeded(field));
            }
        }
        Ok(())
    }
}

fn positive(value: usize, field: &'static str) -> Result<(), WasmConfigError> {
    if value == 0 {
        Err(WasmConfigError::NonPositive(field))
    } else {
        Ok(())
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum WasmConfigError {
    #[error("WASM configuration value {0} must be greater than zero")]
    NonPositive(&'static str),
    #[error("WASM configuration value {0} exceeds the ABI i32 length range")]
    AbiLengthExceeded(&'static str),
    #[error("WASM configuration value {0} exceeds this platform's address range")]
    PlatformRangeExceeded(&'static str),
}

impl TryFrom<&LocalWasmPolicy> for WasmWorkerConfig {
    type Error = WasmConfigError;

    fn try_from(policy: &LocalWasmPolicy) -> Result<Self, Self::Error> {
        let config = Self {
            max_wasm_stack_bytes: platform_size(
                policy.max_wasm_stack_bytes,
                "max_wasm_stack_bytes",
            )?,
        };
        config.validate()?;
        Ok(config)
    }
}

impl TryFrom<&LocalWasmPolicy> for WasmExecutionLimits {
    type Error = WasmConfigError;

    fn try_from(policy: &LocalWasmPolicy) -> Result<Self, Self::Error> {
        let limits = Self {
            max_module_bytes: platform_size(policy.max_module_bytes, "max_module_bytes")?,
            max_input_bytes: platform_size(policy.max_input_bytes, "max_input_bytes")?,
            max_output_bytes: platform_size(policy.max_output_bytes, "max_output_bytes")?,
            max_memory_bytes: platform_size(policy.max_memory_bytes, "max_memory_bytes")?,
            max_table_elements: platform_count(policy.max_table_elements, "max_table_elements")?,
            max_instances: platform_count(policy.max_instances, "max_instances")?,
            max_tables: platform_count(policy.max_tables, "max_tables")?,
            max_memories: platform_count(policy.max_memories, "max_memories")?,
            fuel: policy.fuel,
        };
        limits.validate()?;
        Ok(limits)
    }
}

fn platform_size(value: u64, field: &'static str) -> Result<usize, WasmConfigError> {
    usize::try_from(value).map_err(|_| WasmConfigError::PlatformRangeExceeded(field))
}

fn platform_count(value: u32, field: &'static str) -> Result<usize, WasmConfigError> {
    usize::try_from(value).map_err(|_| WasmConfigError::PlatformRangeExceeded(field))
}

pub struct WasmtimeWorker {
    engine: Engine,
}

pub struct CompiledWasmModule {
    module: Module,
}

impl WasmtimeWorker {
    /// Builds a synchronous Wasmtime engine with fuel metering enabled.
    ///
    /// # Errors
    ///
    /// Returns [`WasmWorkerError`] when configuration is invalid or Wasmtime
    /// rejects the process-level engine configuration.
    pub fn new(config: &WasmWorkerConfig) -> Result<Self, WasmWorkerError> {
        config.validate()?;
        let mut wasmtime = Config::new();
        wasmtime.consume_fuel(true);
        wasmtime.max_wasm_stack(config.max_wasm_stack_bytes);
        let engine = Engine::new(&wasmtime).map_err(|_| WasmWorkerError::RuntimeConfiguration)?;
        Ok(Self { engine })
    }

    /// Compiles a bounded untrusted WebAssembly binary.
    ///
    /// # Errors
    ///
    /// Returns [`WasmWorkerError::ModuleTooLarge`] before compilation when the
    /// configured bound is exceeded, or [`WasmWorkerError::InvalidModule`] for
    /// malformed/unsupported WebAssembly.
    pub fn compile(
        &self,
        wasm: &[u8],
        limits: &WasmExecutionLimits,
    ) -> Result<CompiledWasmModule, WasmWorkerError> {
        limits.validate()?;
        if wasm.len() > limits.max_module_bytes {
            return Err(WasmWorkerError::ModuleTooLarge {
                actual: wasm.len(),
                configured_limit: limits.max_module_bytes,
            });
        }
        let module = Module::new(&self.engine, wasm).map_err(|_| WasmWorkerError::InvalidModule)?;
        Ok(CompiledWasmModule { module })
    }

    /// Executes the BPMP ABI v1 entry point in an isolated store.
    ///
    /// The adapter provides no WASI or host imports. Input is written once into
    /// guest linear memory; output is copied once when it crosses back into the host.
    ///
    /// # Errors
    ///
    /// Returns a typed [`WasmWorkerError`] for invalid ABI, resource exhaustion,
    /// traps, and unsafe guest memory ranges. Guest details/backtraces are not exposed.
    pub fn execute(
        &self,
        module: &CompiledWasmModule,
        input: &[u8],
        limits: &WasmExecutionLimits,
    ) -> Result<Vec<u8>, WasmWorkerError> {
        limits.validate()?;
        if input.len() > limits.max_input_bytes {
            return Err(WasmWorkerError::InputTooLarge {
                actual: input.len(),
                configured_limit: limits.max_input_bytes,
            });
        }

        let mut store = Store::new(&self.engine, ExecutionState::new(limits));
        store.limiter(|state| state);
        store
            .set_fuel(limits.fuel)
            .map_err(|_| WasmWorkerError::RuntimeConfiguration)?;
        let instance = wasmtime::Instance::new(&mut store, &module.module, &[])
            .map_err(|error| classify_instantiation_error(&error))?;
        let abi_version = instance
            .get_typed_func::<(), i32>(&mut store, ABI_VERSION_EXPORT)
            .map_err(|_| WasmWorkerError::MissingOrInvalidExport(ABI_VERSION_EXPORT))?
            .call(&mut store, ())
            .map_err(|error| classify_execution_error(&error))?;
        if abi_version != BPMP_WASM_ABI_VERSION {
            return Err(WasmWorkerError::UnsupportedAbiVersion(abi_version));
        }
        let memory = instance
            .get_memory(&mut store, MEMORY_EXPORT)
            .ok_or(WasmWorkerError::MissingOrInvalidExport(MEMORY_EXPORT))?;
        let allocate = instance
            .get_typed_func::<i32, i32>(&mut store, ALLOC_EXPORT)
            .map_err(|_| WasmWorkerError::MissingOrInvalidExport(ALLOC_EXPORT))?;
        let run = instance
            .get_typed_func::<(i32, i32), (i32, i32)>(&mut store, RUN_EXPORT)
            .map_err(|_| WasmWorkerError::MissingOrInvalidExport(RUN_EXPORT))?;

        let input_length =
            i32::try_from(input.len()).map_err(|_| WasmWorkerError::InvalidGuestMemoryRange)?;
        let input_offset = allocate
            .call(&mut store, input_length)
            .map_err(|error| classify_execution_error(&error))?;
        let input_range = guest_range(input_offset, input_length, memory.data_size(&store))?;
        memory
            .write(&mut store, input_range.start, input)
            .map_err(|_| WasmWorkerError::InvalidGuestMemoryRange)?;

        let (output_offset, output_length) = run
            .call(&mut store, (input_offset, input_length))
            .map_err(|error| classify_execution_error(&error))?;
        let output_length_usize =
            usize::try_from(output_length).map_err(|_| WasmWorkerError::InvalidGuestMemoryRange)?;
        if output_length_usize > limits.max_output_bytes {
            return Err(WasmWorkerError::OutputTooLarge {
                actual: output_length_usize,
                configured_limit: limits.max_output_bytes,
            });
        }
        let output_range = guest_range(output_offset, output_length, memory.data_size(&store))?;
        Ok(memory.data(&store)[output_range].to_vec())
    }
}

fn guest_range(
    offset: i32,
    length: i32,
    available: usize,
) -> Result<Range<usize>, WasmWorkerError> {
    let start = usize::try_from(offset).map_err(|_| WasmWorkerError::InvalidGuestMemoryRange)?;
    let length = usize::try_from(length).map_err(|_| WasmWorkerError::InvalidGuestMemoryRange)?;
    let end = start
        .checked_add(length)
        .filter(|end| *end <= available)
        .ok_or(WasmWorkerError::InvalidGuestMemoryRange)?;
    Ok(start..end)
}

#[derive(Debug)]
struct ExecutionState {
    memory_bytes: usize,
    table_elements: usize,
    instances: usize,
    tables: usize,
    memories: usize,
}

impl ExecutionState {
    const fn new(limits: &WasmExecutionLimits) -> Self {
        Self {
            memory_bytes: limits.max_memory_bytes,
            table_elements: limits.max_table_elements,
            instances: limits.max_instances,
            tables: limits.max_tables,
            memories: limits.max_memories,
        }
    }
}

impl ResourceLimiter for ExecutionState {
    fn memory_growing(
        &mut self,
        _current: usize,
        desired: usize,
        maximum: Option<usize>,
    ) -> anyhow::Result<bool> {
        if desired > self.memory_bytes {
            return Err(ResourceLimitTrap::Memory.into());
        }
        Ok(maximum.is_none_or(|maximum| desired <= maximum))
    }

    fn memory_grow_failed(&mut self, _error: Error) -> anyhow::Result<()> {
        Err(ResourceLimitTrap::Memory.into())
    }

    fn table_growing(
        &mut self,
        _current: usize,
        desired: usize,
        maximum: Option<usize>,
    ) -> anyhow::Result<bool> {
        if desired > self.table_elements {
            return Err(ResourceLimitTrap::Table.into());
        }
        Ok(maximum.is_none_or(|maximum| desired <= maximum))
    }

    fn table_grow_failed(&mut self, _error: Error) -> anyhow::Result<()> {
        Err(ResourceLimitTrap::Table.into())
    }

    fn instances(&self) -> usize {
        self.instances
    }

    fn tables(&self) -> usize {
        self.tables
    }

    fn memories(&self) -> usize {
        self.memories
    }
}

#[derive(Debug, Error)]
enum ResourceLimitTrap {
    #[error("guest linear memory quota exceeded")]
    Memory,
    #[error("guest table quota exceeded")]
    Table,
}

fn classify_execution_error(error: &Error) -> WasmWorkerError {
    if let Some(limit) = error.downcast_ref::<ResourceLimitTrap>() {
        return match limit {
            ResourceLimitTrap::Memory => WasmWorkerError::MemoryLimitExceeded,
            ResourceLimitTrap::Table => WasmWorkerError::TableLimitExceeded,
        };
    }
    if error.downcast_ref::<Trap>() == Some(&Trap::OutOfFuel) {
        WasmWorkerError::FuelExhausted
    } else {
        WasmWorkerError::GuestTrap
    }
}

fn classify_instantiation_error(error: &Error) -> WasmWorkerError {
    match classify_execution_error(error) {
        WasmWorkerError::MemoryLimitExceeded => WasmWorkerError::MemoryLimitExceeded,
        WasmWorkerError::TableLimitExceeded => WasmWorkerError::TableLimitExceeded,
        WasmWorkerError::FuelExhausted => WasmWorkerError::FuelExhausted,
        _ => WasmWorkerError::InstantiationFailed,
    }
}

#[derive(Debug, Error, Clone, Eq, PartialEq)]
pub enum WasmWorkerError {
    #[error(transparent)]
    Configuration(#[from] WasmConfigError),
    #[error("WASM runtime configuration was rejected")]
    RuntimeConfiguration,
    #[error("WASM module size {actual} exceeds configured limit {configured_limit}")]
    ModuleTooLarge {
        actual: usize,
        configured_limit: usize,
    },
    #[error("WASM input size {actual} exceeds configured limit {configured_limit}")]
    InputTooLarge {
        actual: usize,
        configured_limit: usize,
    },
    #[error("WASM output size {actual} exceeds configured limit {configured_limit}")]
    OutputTooLarge {
        actual: usize,
        configured_limit: usize,
    },
    #[error("WASM module is malformed or uses unsupported features")]
    InvalidModule,
    #[error("WASM module could not be instantiated without host capabilities")]
    InstantiationFailed,
    #[error("WASM module is missing or has an invalid required export {0}")]
    MissingOrInvalidExport(&'static str),
    #[error("WASM module uses unsupported BPMP ABI version {0}")]
    UnsupportedAbiVersion(i32),
    #[error("WASM execution exhausted its configured CPU fuel")]
    FuelExhausted,
    #[error("WASM execution exceeded its configured linear memory quota")]
    MemoryLimitExceeded,
    #[error("WASM execution exceeded its configured table quota")]
    TableLimitExceeded,
    #[error("WASM guest trapped during execution")]
    GuestTrap,
    #[error("WASM guest returned an invalid linear-memory range")]
    InvalidGuestMemoryRange,
}

#[cfg(test)]
mod tests {
    use proptest::collection::vec;
    use proptest::prelude::*;
    use proptest::test_runner::{Config as ProptestConfig, TestRunner};

    use super::*;

    const ECHO_MODULE: &str = r#"
        (module
          (memory (export "memory") 1 8)
          (global $heap (mut i32) (i32.const 1024))
          (func (export "bpmp_abi_version") (result i32) i32.const 1)
          (func (export "bpmp_alloc") (param $len i32) (result i32)
            (local $offset i32)
            global.get $heap
            local.tee $offset
            local.get $len
            i32.add
            global.set $heap
            local.get $offset)
          (func (export "bpmp_run") (param $ptr i32) (param $len i32) (result i32 i32)
            local.get $ptr
            local.get $len))
    "#;

    fn worker() -> WasmtimeWorker {
        WasmtimeWorker::new(&WasmWorkerConfig {
            max_wasm_stack_bytes: 512 * 1024,
        })
        .unwrap()
    }

    fn limits() -> WasmExecutionLimits {
        WasmExecutionLimits {
            max_module_bytes: 64 * 1024,
            max_input_bytes: 32 * 1024,
            max_output_bytes: 32 * 1024,
            max_memory_bytes: 2 * 64 * 1024,
            max_table_elements: 1024,
            max_instances: 1,
            max_tables: 1,
            max_memories: 1,
            fuel: 100_000,
        }
    }

    fn compile(worker: &WasmtimeWorker, source: &str) -> CompiledWasmModule {
        worker
            .compile(&wat::parse_str(source).unwrap(), &limits())
            .unwrap()
    }

    #[test]
    fn host_guest_round_trip_is_equivalent_for_bounded_payloads() {
        let worker = worker();
        let module = compile(&worker, ECHO_MODULE);
        let mut runner = TestRunner::new(ProptestConfig::with_cases(100));

        runner
            .run(&vec(any::<u8>(), 0..4096), |payload| {
                let output = worker.execute(&module, &payload, &limits()).unwrap();
                prop_assert_eq!(output, payload);
                Ok(())
            })
            .unwrap();
    }

    #[test]
    fn infinite_loop_exhausts_fuel_without_panicking_host() {
        let worker = worker();
        let module = compile(
            &worker,
            r#"
                (module
                  (memory (export "memory") 1)
                  (func (export "bpmp_abi_version") (result i32) i32.const 1)
                  (func (export "bpmp_alloc") (param i32) (result i32) i32.const 0)
                  (func (export "bpmp_run") (param i32 i32) (result i32 i32)
                    (loop $forever br $forever)
                    unreachable))
            "#,
        );
        let mut constrained = limits();
        constrained.fuel = 1_000;

        assert_eq!(
            worker.execute(&module, b"input", &constrained),
            Err(WasmWorkerError::FuelExhausted)
        );
    }

    #[test]
    fn memory_growth_beyond_quota_returns_resource_error() {
        let worker = worker();
        let module = compile(
            &worker,
            r#"
                (module
                  (memory (export "memory") 1 8)
                  (func (export "bpmp_abi_version") (result i32) i32.const 1)
                  (func (export "bpmp_alloc") (param i32) (result i32)
                    i32.const 2
                    memory.grow
                    drop
                    i32.const 0)
                  (func (export "bpmp_run") (param $ptr i32) (param $len i32) (result i32 i32)
                    local.get $ptr local.get $len))
            "#,
        );
        let mut constrained = limits();
        constrained.max_memory_bytes = 64 * 1024;

        assert_eq!(
            worker.execute(&module, b"input", &constrained),
            Err(WasmWorkerError::MemoryLimitExceeded)
        );
    }

    #[test]
    fn guest_panic_is_isolated_as_safe_typed_error() {
        let worker = worker();
        let module = compile(
            &worker,
            r#"
                (module
                  (memory (export "memory") 1)
                  (func (export "bpmp_abi_version") (result i32) i32.const 1)
                  (func (export "bpmp_alloc") (param i32) (result i32) i32.const 0)
                  (func (export "bpmp_run") (param i32 i32) (result i32 i32)
                    unreachable))
            "#,
        );

        assert_eq!(
            worker.execute(&module, b"input", &limits()),
            Err(WasmWorkerError::GuestTrap)
        );
    }

    #[test]
    fn module_and_payload_bounds_fail_before_unsafe_allocation() {
        let worker = worker();
        let wasm = wat::parse_str(ECHO_MODULE).unwrap();
        let mut module_limited = limits();
        module_limited.max_module_bytes = wasm.len() - 1;
        assert!(matches!(
            worker.compile(&wasm, &module_limited),
            Err(WasmWorkerError::ModuleTooLarge { .. })
        ));

        let module = worker.compile(&wasm, &limits()).unwrap();
        let mut input_limited = limits();
        input_limited.max_input_bytes = 2;
        assert!(matches!(
            worker.execute(&module, b"abc", &input_limited),
            Err(WasmWorkerError::InputTooLarge { .. })
        ));

        let mut output_limited = limits();
        output_limited.max_output_bytes = 2;
        assert!(matches!(
            worker.execute(&module, b"abc", &output_limited),
            Err(WasmWorkerError::OutputTooLarge { .. })
        ));
    }

    #[test]
    fn unsupported_abi_version_is_rejected_before_payload_transfer() {
        let worker = worker();
        let module = compile(
            &worker,
            &ECHO_MODULE.replace("(result i32) i32.const 1", "(result i32) i32.const 2"),
        );

        assert_eq!(
            worker.execute(&module, b"input", &limits()),
            Err(WasmWorkerError::UnsupportedAbiVersion(2))
        );
    }

    #[test]
    fn module_cannot_acquire_unconfigured_host_capabilities() {
        let worker = worker();
        let module = compile(
            &worker,
            r#"
                (module
                  (import "wasi_snapshot_preview1" "fd_write" (func $fd_write))
                  (memory (export "memory") 1)
                  (func (export "bpmp_abi_version") (result i32) i32.const 1)
                  (func (export "bpmp_alloc") (param i32) (result i32) i32.const 0)
                  (func (export "bpmp_run") (param $ptr i32) (param $len i32) (result i32 i32)
                    local.get $ptr local.get $len))
            "#,
        );

        assert_eq!(
            worker.execute(&module, b"input", &limits()),
            Err(WasmWorkerError::InstantiationFailed)
        );
    }
}
