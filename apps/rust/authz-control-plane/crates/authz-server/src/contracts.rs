pub mod bpmp {
    pub mod authorization {
        pub mod v1 {
            include!(concat!(env!("OUT_DIR"), "/bpmp.authorization.v1.rs"));
        }
    }
    pub mod configuration {
        pub mod v1 {
            include!(concat!(env!("OUT_DIR"), "/bpmp.configuration.v1.rs"));
        }
    }

    pub mod tenancy {
        pub mod v1 {
            include!(concat!(env!("OUT_DIR"), "/bpmp.tenancy.v1.rs"));
        }
    }
}
