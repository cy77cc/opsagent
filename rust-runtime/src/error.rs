use thiserror::Error;

#[derive(Error, Debug)]
#[allow(dead_code)]
pub enum PluginError {
    #[error("Configuration error: {0}")]
    Config(String),

    #[error("Execution error: {0}")]
    Execution(String),

    #[error("Resource error: {0}")]
    Resource(String),

    #[error("Unsupported on this system: {0}")]
    Unsupported(String),

    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
}

impl PluginError {
    pub fn rpc_code(&self) -> i32 {
        match self {
            Self::Config(_) => -32602,
            Self::Execution(_) => -32000,
            Self::Resource(_) => -32001,
            Self::Unsupported(_) => -32002,
            Self::Io(_) => -32603,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_error_display() {
        let e = PluginError::Config("bad input".into());
        assert_eq!(e.to_string(), "Configuration error: bad input");
    }

    #[test]
    fn test_rpc_codes() {
        assert_eq!(PluginError::Config("".into()).rpc_code(), -32602);
        assert_eq!(PluginError::Execution("".into()).rpc_code(), -32000);
        assert_eq!(PluginError::Resource("".into()).rpc_code(), -32001);
        assert_eq!(PluginError::Unsupported("".into()).rpc_code(), -32002);
    }

    #[test]
    fn test_io_error_conversion() {
        let io_err = std::io::Error::new(std::io::ErrorKind::NotFound, "file not found");
        let e: PluginError = io_err.into();
        assert_eq!(e.rpc_code(), -32603);
        assert!(e.to_string().contains("IO error"));
    }
}
