# AWS EDP Shortfall Calculator

[![CI](https://github.com/kujon/aws-shortfall/actions/workflows/ci.yml/badge.svg)](https://github.com/kujon/aws-shortfall/actions/workflows/ci.yml)
[![Release](https://github.com/kujon/aws-shortfall/actions/workflows/release.yml/badge.svg)](https://github.com/kujon/aws-shortfall/actions/workflows/release.yml)

A tool to calculate your AWS Enterprise Discount Program (EDP) shortfall by analyzing your AWS Cost Explorer data and automatically detecting which marketplace products are deployed on AWS infrastructure.

## Features

✅ **Automatic Detection** - Uses AWS Marketplace Discovery API to automatically detect which products are deployed on AWS  
✅ **Accurate Calculation** - Excludes non-AWS hosted products from EDP-eligible spend  
✅ **EDP Discount Handling** - Adds back EDP discount credits to calculate true spend against commitment  
✅ **Extended Support** - Automatically excludes Extended Support charges  
✅ **Marketplace Classification** - Classifies products by delivery method (SaaS, AMI, Container, Data)  
✅ **Monthly Breakdown** - Shows spending trends month-by-month  
✅ **Projection** - Linear extrapolation of spending for the full EDP period  

## Installation

### From Binary (Recommended)

Download the latest release for your platform from the [releases page](https://github.com/kujon/aws-shortfall/releases):

```bash
# Linux/macOS
curl -LO https://github.com/kujon/aws-shortfall/releases/latest/download/aws-shortfall-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m).tar.gz
tar xzf aws-shortfall-*.tar.gz
chmod +x aws-shortfall-*

# Move to PATH (optional)
sudo mv aws-shortfall-* /usr/local/bin/aws-shortfall
```

### From Source

```bash
git clone https://github.com/kujon/aws-shortfall.git
cd aws-shortfall
go build -o aws-shortfall main.go
```

## Usage

### Basic Usage

```bash
# Use default AWS profile
./aws-shortfall

# Use specific AWS profile
./aws-shortfall --profile production

# Custom EDP commitment and period
./aws-shortfall \
  --commitment 1000000 \
  --start 2024-01-01 \
  --end 2024-12-31 \
  --profile production
```

### Command-Line Options

```
  -commitment float
        EDP committed spend in USD (default 1000000)
  -end string
        EDP period end (YYYY-MM-DD) (default "2024-12-31")
  -profile string
        AWS profile to use
  -start string
        EDP period start (YYYY-MM-DD) (default "2024-01-01")
  -version
        Show version information
```

## How It Works

### 1. Cost Data Collection
- Queries AWS Cost Explorer for unblended costs during the EDP period
- Excludes **Tax** charges (not EDP-eligible)
- Excludes **Extended Support** charges (not EDP-eligible)

### 2. EDP Discount Handling
- Fetches EDP discount credits (`EdpDiscount`, `PrivateRateDiscount`, `BundledDiscount`)
- Adds these back to calculate true spend against commitment

### 3. Marketplace Product Detection
- Calls AWS Marketplace Agreement API to get active subscriptions
- Calls **AWS Marketplace Discovery API** to check "Deployed on AWS" status
- Matches products by dimension keys and product names
- Automatically excludes products NOT deployed on AWS

### 4. Calculation
```
EDP-Eligible Spend = Total Costs - Extended Support - Non-AWS Marketplace - EDP Discount Credits
Shortfall = EDP Commitment - EDP-Eligible Spend
```

## Example Output

```
=== AWS EDP Shortfall Calculator ===

EDP Period:     2024-01-01 to 2024-12-31
Data through:   2024-12-31
Commitment:     $1,000,000.00

Month    Eligible Spend
-----    --------------
2024-01  $75,234.56
2024-02  $82,145.33
2024-03  $79,821.45
...

--- Marketplace Products ---
Product                                     Cost       Delivery  On AWS?  Counts?
-------                                     ----       --------  -------  -------
Observability Platform A                    $85,234.12  SaaS      true     YES
Content Management Platform B               $12,345.67  SaaS      true     YES
Analytics Platform C                        $8,750.00   SaaS      false    NO
Development Tool D                          $5,420.00   SaaS      false    NO
Analytics Platform E                        $4,200.00   SaaS      false    NO
AI Model Provider F                         $3,456.78   SaaS      true     YES
...

Total eligible spend (excl. Tax & Extended Support):  $950,000.00
EDP discount credits added back:                      $0.00
Marketplace (not on AWS) excluded:                    -$18,370.00
EDP-countable spend:                                  $931,630.00

SHORTFALL:  $68,370.00
Coverage:   93.2%
```

## Required AWS Permissions

The tool requires the following IAM permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage",
        "aws-marketplace:SearchAgreements",
        "aws-marketplace:GetAgreementTerms",
        "aws-marketplace:ViewSubscriptions"
      ],
      "Resource": "*"
    }
  ]
}
```

## Development

### Prerequisites

- Go 1.26 or later
- AWS credentials configured

### Building

```bash
go build -o aws-shortfall main.go
```

### Running Tests

```bash
go test -v ./...
```

### Code Quality

```bash
# Format code
go fmt ./...

# Run linter
go vet ./...

# Check imports
goimports -w .

# Tidy modules
go mod tidy
```

## CI/CD

The project uses GitHub Actions for continuous integration and deployment:

- **CI Workflow** - Runs on every push and PR
  - Code formatting checks (`gofmt`)
  - Import organization checks (`goimports`)
  - Static analysis (`go vet`)
  - Module tidiness check (`go mod tidy`)
  - Builds for all platforms
  - Runs tests with race detection
  
- **Release Workflow** - Triggered on version tags
  - Builds binaries for Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)
  - Creates GitHub release with changelog
  - Uploads pre-built binaries

### Creating a Release

```bash
# Tag a new version
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0

# GitHub Actions will automatically:
# 1. Build binaries for all platforms
# 2. Create a GitHub release
# 3. Upload binaries to the release
```

## Troubleshooting

### "AccessDenied" errors

Make sure your AWS credentials have the required permissions listed above.

### "Could not auto-detect deployment status"

The tool falls back to treating all marketplace products as deployed on AWS if the Discovery API fails. This is a conservative approach to avoid underreporting EDP spend.

## Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

MIT License - see LICENSE file for details

## Acknowledgments

- Uses the official AWS SDK for Go v2
- Leverages AWS Marketplace Discovery API for accurate product classification
