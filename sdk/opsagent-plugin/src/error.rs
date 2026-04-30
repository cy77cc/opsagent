use thiserror::Error;

#[derive(Error, Debug)]
pub enum PluginError {
    #[error("configuration error: {0}")]
    Config(String),
    #[error("execution error: {0}")]
    Execution(String),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("JSON error: {0}")]
    Json(#[from] serde_json::Error),
}

pub type Result<T> = std::result::Result<T, PluginError>;
